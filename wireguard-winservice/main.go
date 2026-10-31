package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/drakovic23/wg-vpn/wireguard-winservice/winpipe"
	"github.com/kardianos/service"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

type program struct{ quit chan struct{} }

var logger service.Logger

func main() {

	cfg := &service.Config{
		Name:        "WG-Client",
		DisplayName: "WG-Client",
		Description: "WG Service",
	}
	s, err := service.New(&program{}, cfg)
	if err != nil {
		log.Fatal(err)
	}

	logger, err = s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}

	logger.Infof("Starting WG-Client")
	if len(os.Args) > 1 {
		if err := service.Control(s, os.Args[1]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := s.Run(); err != nil { // works as a service or in a console
		log.Fatal(err)
	}
}

func (p *program) Start(s service.Service) error {
	p.quit = make(chan struct{})
	go p.run() // Start must not block
	return nil
}
func (p *program) run() {

	fmt.Println("Starting test client")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	const pipeName = `\\.\pipe\\testPipe`
	srv, err := winpipe.NewWindowsPipe(pipeName)
	if err != nil {
		log.Fatalf("Error creating pipe: %v", err)
	}
	srv.SetState(winpipe.Disconnected)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.RunPipeListener(ctx)
	}()

	log.Println("Listening on ", pipeName)

	var tunnel *Tunnel
	defer func() {
		if tunnel != nil {
			tunnel.Close()
		}
	}()

	for {
		var tunnelDone <-chan struct{}
		if tunnel != nil {
			tunnelDone = tunnel.Done()
		}

		select {
		case cmd := <-srv.Events():
			switch c := cmd.(type) {
			case winpipe.ConnectCmd:
				if c.Req.AccessToken == "" {
					logger.Infof("Ignoring request with empty access token")
					continue
				}

				srv.SetState(winpipe.Connecting)

				k := generateKeys()
				cfg, err := authCall(c.Req, k)
				if err != nil {
					logger.Errorf("Error during authentication %v", err)

					if tunnel != nil {
						srv.SetState(winpipe.Connected) // the previous tunnel is still up
					} else {
						srv.SetState(winpipe.ConnectionError)
					}
					continue
				}

				if tunnel == nil {
					tunnel, err = startWireguard(cfg)
				} else if err = tunnel.Configure(cfg); err != nil {
					tunnel.Close()
					tunnel = nil
				}
				if err != nil {
					logger.Infof("Applying config failed: %v", err)
					srv.SetState(winpipe.ConnectionError)
					continue
				}
				srv.SetState(winpipe.Connected)

			case winpipe.DisconnectCmd:
				if tunnel != nil {
					tunnel.Close()
					tunnel = nil
				}
				srv.SetState(winpipe.Disconnected)

			default:
				logger.Infof("Unknown command type %T", c)
			}

		case <-tunnelDone:
			logger.Info("WireGuard device closed unexpectedly")
			srv.SetState(winpipe.Error)
			tunnel = nil

		case err := <-errCh:
			if err != nil {
				logger.Infof("Pipe listener error: %v", err)
			}
			return

		case <-ctx.Done():
			logger.Infof("Shutting down")
			<-errCh
			return
		}
	}
}

func (p *program) Stop(s service.Service) error {
	close(p.quit)
	return nil
}

type Tunnel struct {
	dev  *device.Device
	luid winipcfg.LUID
}

func (t *Tunnel) Configure(cfg deviceConfig) error {
	prvKey, err := wgtypes.ParseKey(cfg.PrivateKey)
	if err != nil {
		return err
	}
	pubKey, err := wgtypes.ParseKey(cfg.PublicKey)
	if err != nil {
		return err
	}

	endPoint, err := resolveEndpoint(context.Background(), cfg.EndPoint)
	if err != nil {
		return err
	}
	// Note replace peers is set to true so peers do not pile up if a different config is used
	uapi := fmt.Sprintf(
		"private_key=%s\nreplace_peers=true\npublic_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=%d\n",
		hex.EncodeToString(prvKey[:]), hex.EncodeToString(pubKey[:]), endPoint, cfg.AllowedIp, cfg.KeepAlive)
	if err := t.dev.IpcSet(uapi); err != nil {
		return fmt.Errorf("IpcSet: %w", err)
	}

	ip, err := netip.ParsePrefix(cfg.TunnelIp + "/32")
	if err != nil {
		return fmt.Errorf("parse tunnel IP: %w", err)
	}
	if err := t.luid.SetIPAddresses([]netip.Prefix{ip}); err != nil { // replaces existing addresses
		return fmt.Errorf("set adapter IP: %w", err)
	}

	dest, err := netip.ParsePrefix(cfg.AllowedIp)
	if err != nil {
		return fmt.Errorf("parse allowed IP: %w", err)
	}
	// this will flushe the interface's routes first, so old config routes go away.
	return t.luid.SetRoutes([]*winipcfg.RouteData{
		{Destination: dest, NextHop: netip.IPv4Unspecified()},
	})
}

func (t *Tunnel) Done() <-chan struct{} { return t.dev.Wait() }
func (t *Tunnel) Close()                { t.dev.Close() }

func generateKeys() Keys {
	prv, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		log.Fatalf("Error generating private key: %v", err)
	}

	keys := Keys{
		PrivateKey: prv.String(),
		PublicKey:  prv.PublicKey().String(),
	}

	return keys
}

type Keys struct {
	PrivateKey string
	PublicKey  string
}

func startWireguard(cfg deviceConfig) (*Tunnel, error) {
	const interfaceName = "Test-Interface"
	devLogger := device.NewLogger(device.LogLevelVerbose, fmt.Sprintf("(%s)", interfaceName))

	newTun, err := tun.CreateTUN(interfaceName, 0)
	if err != nil {
		return nil, fmt.Errorf("create TUN: %w", err)
	}

	dev := device.NewDevice(newTun, conn.NewDefaultBind(), devLogger)
	t := &Tunnel{
		dev:  dev,
		luid: winipcfg.LUID(newTun.(*tun.NativeTun).LUID()),
	}

	if err := dev.Up(); err != nil {
		dev.Close() // also closes the Wintun adapter
		return nil, fmt.Errorf("bring device up: %w", err)
	}
	if err := t.Configure(cfg); err != nil {
		dev.Close()
		return nil, err
	}

	logger.Infof("Device started")
	return t, nil
}

type addPeerRequest struct {
	PublicKey string `json:"public_key"`
}

type addPeerResponse struct {
	ServerPublicKey string `json:"server_public_key"`
	ListenPort      int    `json:"listen_port"`
	TunnelIP        string `json:"tunnel_ip"`
	AllowedIPs      string `json:"allowed_ip"`
}

type deviceConfig struct {
	PrivateKey string
	PublicKey  string
	EndPoint   string
	AllowedIp  string
	TunnelIp   string
	KeepAlive  int
}

func authCall(r winpipe.ConnectionRequest, k Keys) (deviceConfig, error) {
	// Make a call to the host
	var scheme string = "https://"

	if r.UseHttps == false {
		scheme = "http://"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 25 * time.Second}

	peerReq := addPeerRequest{
		PublicKey: k.PublicKey,
	}

	payload, _ := json.Marshal(peerReq)

	// TODO: this won't work with ipv6
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		scheme+r.AuthHost+":"+r.ServerPort+"/peer/add", bytes.NewReader(payload))

	if err != nil {
		return deviceConfig{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.AccessToken)

	resp, err := client.Do(req)

	if err != nil {
		return deviceConfig{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Unexpected status %d: %s\n", resp.StatusCode, body)
		return deviceConfig{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	var peerResp addPeerResponse
	if err := json.NewDecoder(resp.Body).Decode(&peerResp); err != nil {
		return deviceConfig{}, err
	}

	wgPort := strconv.Itoa(peerResp.ListenPort)
	endP := r.AuthHost + ":" + wgPort

	c := deviceConfig{
		PrivateKey: k.PrivateKey,
		PublicKey:  peerResp.ServerPublicKey,
		EndPoint:   endP,
		TunnelIp:   peerResp.TunnelIP,
		AllowedIp:  peerResp.AllowedIPs,
		KeepAlive:  25,
	}

	logger.Infof("Added peer to device")
	return c, nil
}

func resolveEndpoint(ctx context.Context, endpoint string) (string, error) {
	// Endpoints are often "host:port"; split so only the host is looked up.
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		host, port = endpoint, "" // no port in the endpoint
	}

	cloudflare := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, "1.1.1.1:53")
		},
	}

	cfCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ips, err := cloudflare.LookupIP(cfCtx, "ip4", host)
	cancel()

	if err != nil {
		// Fall back to the OS resolver
		osCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err = net.DefaultResolver.LookupIP(osCtx, "ip4", host)
		cancel()
		if err != nil {
			return "", fmt.Errorf("resolving %s: %w", host, err)
		}
	}

	ip := ips[0].String()
	if port == "" {
		return ip, nil
	}
	return net.JoinHostPort(ip, port), nil
}

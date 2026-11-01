package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-oidc"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

var TenantId = ""
var ApiAppId = ""

type entraClaims struct {
	TenantID string   `json:"tid"`
	Roles    []string `json:"roles"` // app roles
	Scopes   string   `json:"scp"`   // delegated scopes, space-separated
	Version  string   `json:"ver"`
	ClientID string   `json:"azp"`
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

type wgClient struct {
	wg          *wgctrl.Client
	iface       string
	mu          sync.Mutex
	ifaceIp     net.IP
	ifaceSubNet *net.IPNet
}

const maxBodyBytes = 64 * 1024
const maxHeaderBytes = 1024 * 1024

func main() {
	var err error
	TenantId, ApiAppId, err = readConfig("cfg.wgc")
	if err != nil {
		log.Fatal(err)
	}

	iface := flag.String("interface", "wg0", "wireguard interface to use")
	port := flag.Int("port", 443, "the server port to listen on")
	domain := flag.String("domain", "", "public hostname for automatic Let's Encrypt TLS")
	staging := flag.Bool("staging", false, "use the Let's Encrypt staging CA (untrusted certs, for testing)")

	flag.Parse()

	if *iface == "" {
		*iface = "wg0"
	}

	if *domain == "" {
		log.Fatal("-domain flag is required")
	}

	wctrl, err := wgctrl.New()
	if err != nil {
		log.Fatalf("Error starting wgctrl client.")
	}
	defer wctrl.Close()

	ip, subnet, err := interfaceSubnet(*iface)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Get interfaces
	found := false
	interfaces, err := wctrl.Devices()

	if errors.Is(err, os.ErrNotExist) {
		log.Fatalf("Interface %s does not exist, or isn't a wireguard interface", *iface)
	} else if err != nil {
		log.Fatalf("Error finding interfaces: %v", err)
	}

	for _, d := range interfaces {
		if d.Name == *iface {
			found = true
			break
		}
	}

	// Don't handle requests unless a valid interface is found
	if found != true {
		log.Fatalf("Error finding specified interface. %v \n Unable to start wgctrl client.", *iface)
	}

	wgc := wgClient{
		wg:          wctrl,
		iface:       *iface,
		ifaceIp:     ip,
		ifaceSubNet: subnet,
	}

	cacheDir := "certs"
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(*domain),
	}
	if *staging {
		m.Client = &acme.Client{DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory"}
		cacheDir = "certs-staging"
	}
	m.Cache = autocert.DirCache(cacheDir)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /peer/add", wgc.addPeerHandler)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", *port),
		Handler:           http.MaxBytesHandler(mux, maxBodyBytes),
		TLSConfig:         m.TLSConfig(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    maxHeaderBytes,
	}

	go func() {
		log.Printf("listening on %s for %s (staging=%v)", srv.Addr, *domain, *staging)
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func handleTokenValidation(r *http.Request) error {
	rawToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || rawToken == "" {
		return errors.New("missing bearer token")
	}
	verifier, err := newVerifier(r.Context())

	if err != nil {
		// http.Error(w, "Error", http.StatusInternalServerError)
		return err
	}

	err = validate(r.Context(), verifier, rawToken, "Device.Enroll")

	if err != nil {
		log.Printf("error during token validation: %s\n", err.Error())
		return err
	}
	return nil
}

// TODO: Can be cached
func newVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	p, err := oidc.NewProvider(ctx, "https://login.microsoftonline.com/"+TenantId+"/v2.0")
	if err != nil {
		return nil, err
	}

	return p.Verifier(&oidc.Config{ClientID: ApiAppId}), nil
}

func validate(ctx context.Context, v *oidc.IDTokenVerifier, rawToken string, role string) error {
	tok, err := v.Verify(ctx, rawToken) // signature, iss, aud, exp, nbf

	if err != nil {
		return err
	}
	var c entraClaims
	if err := tok.Claims(&c); err != nil {
		return err
	}
	if c.TenantID != TenantId {
		return fmt.Errorf("wrong tenant %q", c.TenantID)
	}
	if !slices.Contains(c.Roles, role) {
		return fmt.Errorf("missing role %q", role)
	}

	log.Println("validation successful")
	return nil
}

func (s *wgClient) addPeerHandler(w http.ResponseWriter, r *http.Request) {
	var addPeerReq addPeerRequest

	err := json.NewDecoder(r.Body).Decode(&addPeerReq)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	err = handleTokenValidation(r)

	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Token is valid, find a free IP
	wgInterface, err := s.wg.Device(s.iface)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Printf("Internal error finding device interface: %v", err)
		return
	}

	s.mu.Lock()
	tunIp, err := getNextIP(wgInterface, addPeerReq.PublicKey, s.ifaceIp, *s.ifaceSubNet)
	if err != nil {
		log.Printf("Error finding next IP, %v", err)
	} else {
		err = addPeer(s.wg, s.iface, addPeerReq.PublicKey, tunIp.String())
	}
	s.mu.Unlock()

	if err != nil {
		http.Error(w, "Error", http.StatusInternalServerError)
		log.Printf("Internal error adding peer: %v", w)
		return
	}

	//AllowedIp, err := getSubnetAddress(tunIp.String())
	addPeerResp := addPeerResponse{
		ServerPublicKey: wgInterface.PublicKey.String(),
		ListenPort:      wgInterface.ListenPort,
		TunnelIP:        tunIp.String(),
		AllowedIPs:      "0.0.0.0/0",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(addPeerResp)
}

// var test = "10.8.0.3"
func getNextIP(d *wgtypes.Device, pubKey string, serverIp net.IP, subnet net.IPNet) (net.IP, error) {
	// Check if the public key is  already assigned
	assignedIps := []net.IPNet{{IP: serverIp, Mask: net.CIDRMask(32, 32)}}

	for _, p := range d.Peers {
		if p.PublicKey.String() == pubKey {
			for _, a := range p.AllowedIPs {
				if subnet.Contains(a.IP) { // Peer already has an IP assigned
					return a.IP, nil
				}
			}
		}
		assignedIps = append(assignedIps, p.AllowedIPs...)
	}

	base4 := subnet.IP.To4()
	if base4 == nil {
		return nil, fmt.Errorf("%s is not an IPv4 subnet", subnet.String())
	}
	ones, bits := subnet.Mask.Size()
	size := 1 << (bits - ones)
	base := binary.BigEndian.Uint32(subnet.IP)

	// Find a free IP
	for i := 1; i < size-1; i++ { //skip .0 and .255
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, base+uint32(i))

		free := true

		for _, t := range assignedIps {
			if t.Contains(ip) {
				free = false
				break
			}
		}

		if free {
			return ip, nil
		}

	}

	return nil, fmt.Errorf("No Free addresses in %s", serverIp)
}

func addPeer(wgc *wgctrl.Client, iface, pubKey, tunnelIp string) error {
	pub, err := wgtypes.ParseKey(pubKey)

	if err != nil {
		return fmt.Errorf("Invalid public key: %w", err)
	}

	_, allowed, err := net.ParseCIDR(tunnelIp + "/32")
	if err != nil {
		return fmt.Errorf("Invalid tunnel IP: %w", err)
	}

	return wgc.ConfigureDevice(iface, wgtypes.Config{
		ReplacePeers: false,
		Peers: []wgtypes.PeerConfig{{
			PublicKey:         pub,
			ReplaceAllowedIPs: true,
			AllowedIPs:        []net.IPNet{*allowed},
		}},
	})
}

func interfaceSubnet(name string) (net.IP, *net.IPNet, error) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil, nil, err
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, nil, err
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			return ip4, &net.IPNet{IP: ip4.Mask(ipnet.Mask), Mask: ipnet.Mask}, nil
		}
	}
	return nil, nil, fmt.Errorf("%s has no IPv4 address", name)
}

func readConfig(filename string) (tenantID, clientID string, err error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", "", err
	}
	text := strings.TrimPrefix(string(data), "\uFEFF") // strip UTF-8 BOM

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		switch key {
		case "tenant_id":
			tenantID = value
		case "api_app_id":
			clientID = value
		}
	}

	if tenantID == "" || clientID == "" {
		return "", "", fmt.Errorf("%s: tenant_id and api_app_id must both be set", filename)
	}
	return tenantID, clientID, nil
}

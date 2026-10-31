package winpipe

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

type PipeServer struct {
	name     string
	Listener net.Listener
	cmd      chan Command
	mu       sync.RWMutex
	state    State
}

type Command interface{ isCommand() }
type ConnectCmd struct{ Req ConnectionRequest }
type DisconnectCmd struct{}

func (ConnectCmd) isCommand()    {}
func (DisconnectCmd) isCommand() {}

type State string

const (
	Connected       State = "connected"
	Disconnected    State = "disconnected"
	Error           State = "error"
	ConnectionError State = "connection_error"
	Connecting      State = "connecting"
)

type Config struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
	EndPoint   string `json:"endpoint"`
	AllowedIp  string `json:"allowedIp"`
	KeepAlive  int    `json:"keepAlive"`
}

func NewWindowsPipe(pipeName string) (*PipeServer, error) {
	newPipe := new(PipeServer)
	var err error

	newPipe.name = pipeName
	newPipe.Listener, err = createPipe(pipeName)

	if err != nil {
		return nil, err
	}

	newPipe.cmd = make(chan Command)

	return newPipe, nil
}

func (p *PipeServer) Events() <-chan Command {
	return p.cmd
}

func (s *PipeServer) RunPipeListener(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /connect", s.handleConnectionReq)
	mux.HandleFunc("GET /state", s.handleGetState)
	mux.HandleFunc("POST /disconnect", s.handleDisconnect)

	srv := &http.Server{
		Handler: mux,

		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	err := srv.Serve(s.Listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}

	close(s.cmd)
	return err
}

type ConnectionRequest struct {
	AccessToken string `json:"accessToken"`
	AuthHost    string `json:"authHost"`
	UseHttps    bool   `json:"useHttps"`
	ServerPort  string `json:"serverPort"`
}

func (s *PipeServer) SetState(st State) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}
func (s *PipeServer) handleGetState(w http.ResponseWriter, r *http.Request) {
	log.Println("Processing call to handleGetState")

	s.mu.RLock()
	st := s.state
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(st)
	if err != nil {
		log.Printf("error encoding state: %v", err)
		return
	}

	log.Println("Responded to GetState request")
}

func (s *PipeServer) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.send(w, r, DisconnectCmd{})
}
func (s *PipeServer) send(w http.ResponseWriter, r *http.Request, cmd Command) {
	select {
	case s.cmd <- cmd:
		w.WriteHeader(http.StatusAccepted)
	case <-r.Context().Done():
		http.Error(w, "service shutting down", http.StatusServiceUnavailable)
	}
}
func (s *PipeServer) handleConnectionReq(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req ConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid config: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.send(w, r, ConnectCmd{Req: req})
}

// SYSTEM and users in admin group: full control.
// Users: read and write. Network logons: denied.
const pipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;BU)"

// Note that this requires the pipe to not already exist and fails if the pipe is squatted
func createPipe(pipeName string) (net.Listener, error) {
	// TODO: Validate pipe name here
	l, err := winio.ListenPipe(pipeName, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL}) // Creates the actual pipe

	if err != nil {
		return nil, err
	}

	return l, nil
}

func PrintConfig(c Config) {
	log.Printf("PrivateKey: %v | PublicKey: %v | AllowedIP: %v | Endpoint: %v\n", c.PrivateKey, c.PublicKey, c.AllowedIp, c.EndPoint)
}

func PrintConnReq(r ConnectionRequest) {
	log.Printf("Token: %v | Host: %v\n", r.AccessToken, r.AuthHost)

}

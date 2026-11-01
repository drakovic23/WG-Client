package winpipe

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// The host is ignored because the client always dials the pipe
const pipeURL = "http://pipe"

const testTimeout = 5 * time.Second

type testPipe struct {
	name   string
	srv    *PipeServer
	client *http.Client
	events chan Command // closed when the server stops
	done   chan error   // receives the RunPipeListener result
	stop   context.CancelFunc
}

func uniquePipeName() string {
	return fmt.Sprintf(`\\.\pipe\wg-vpn-test-%d`, time.Now().UnixNano())
}

// startTestPipe runs a real named pipe server and returns an HTTP client for it.
func startTestPipe(t *testing.T) *testPipe {
	t.Helper()

	name := uniquePipeName()
	srv, err := NewWindowsPipe(name)
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &testPipe{
		name: name,
		srv:  srv,
		client: &http.Client{
			Timeout: testTimeout,
			Transport: &http.Transport{
				DisableKeepAlives: true,
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return winio.DialPipeContext(ctx, name)
				},
			},
		},
		events: make(chan Command, 8),
		done:   make(chan error, 1),
		stop:   cancel,
	}

	go func() { p.done <- srv.RunPipeListener(ctx) }()

	// Stands in for the service loop, which reads commands from Events
	go func() {
		for cmd := range srv.Events() {
			p.events <- cmd
		}
		close(p.events)
	}()

	t.Cleanup(cancel)
	return p
}

func (p *testPipe) nextCommand(t *testing.T) Command {
	t.Helper()
	select {
	case cmd, ok := <-p.events:
		if !ok {
			t.Fatal("events closed before a command arrived")
		}
		return cmd
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for a command")
		return nil
	}
}

func TestPipeRoundTrip(t *testing.T) {
	pipe := startTestPipe(t)

	t.Run("get state", func(t *testing.T) {
		pipe.srv.SetState(Connecting)

		resp, err := pipe.client.Get(pipeURL + "/state")
		if err != nil {
			t.Fatalf("GET /state: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got State
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode state: %v", err)
		}
		if got != Connecting {
			t.Errorf("state: got %q, want %q", got, Connecting)
		}
	})

	t.Run("connect", func(t *testing.T) {
		body := `{"accessToken":"token","authHost":"vpn.example.com","useHttps":true,"serverPort":"443"}`

		resp, err := pipe.client.Post(pipeURL+"/connect", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST /connect: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusAccepted)
		}

		cmd, ok := pipe.nextCommand(t).(ConnectCmd)
		if !ok {
			t.Fatal("expected a ConnectCmd")
		}
		want := ConnectionRequest{
			AccessToken: "token",
			AuthHost:    "vpn.example.com",
			UseHttps:    true,
			ServerPort:  "443",
		}
		if cmd.Req != want {
			t.Errorf("request: got %+v, want %+v", cmd.Req, want)
		}
	})

	t.Run("disconnect", func(t *testing.T) {
		resp, err := pipe.client.Post(pipeURL+"/disconnect", "application/json", nil)
		if err != nil {
			t.Fatalf("POST /disconnect: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusAccepted)
		}
		if _, ok := pipe.nextCommand(t).(DisconnectCmd); !ok {
			t.Fatal("expected a DisconnectCmd")
		}
	})

	routes := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "GET on connect", method: http.MethodGet, path: "/connect", wantStatus: http.StatusMethodNotAllowed},
		{name: "GET on disconnect", method: http.MethodGet, path: "/disconnect", wantStatus: http.StatusMethodNotAllowed},
		{name: "POST on state", method: http.MethodPost, path: "/state", wantStatus: http.StatusMethodNotAllowed},
		{name: "unknown path", method: http.MethodGet, path: "/unknown", wantStatus: http.StatusNotFound},
	}

	for _, tt := range routes {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, pipeURL+tt.path, nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}

			resp, err := pipe.client.Do(req)
			if err != nil {
				t.Fatalf("send request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status: got %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestPipeShutdown(t *testing.T) {
	pipe := startTestPipe(t)

	pipe.stop()

	select {
	case err := <-pipe.done:
		if err != nil {
			t.Fatalf("RunPipeListener returned an error: %v", err)
		}
	case <-time.After(testTimeout + time.Second):
		t.Fatal("timed out waiting for the listener to stop")
	}

	// The service loop relies on this channel closing when the listener stops
	select {
	case _, ok := <-pipe.events:
		if ok {
			t.Fatal("expected events to be closed, got a command")
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for events to close")
	}

	// A restarted service has to be able to create the pipe again
	again, err := NewWindowsPipe(pipe.name)
	if err != nil {
		t.Fatalf("recreate pipe after shutdown: %v", err)
	}
	again.Listener.Close()
}

func TestNewWindowsPipeRejectsExistingName(t *testing.T) {
	name := uniquePipeName()

	first, err := NewWindowsPipe(name)
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer first.Listener.Close()

	// Another process could otherwise squat on the name and read tokens sent to it
	second, err := NewWindowsPipe(name)
	if err == nil {
		second.Listener.Close()
		t.Fatal("expected an error for a pipe name already in use")
	}
}

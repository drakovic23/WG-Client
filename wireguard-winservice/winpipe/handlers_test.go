package winpipe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer skips the real pipe, so handlers can be called directly.
// The buffer lets a handler finish without something reading the command.
func newTestServer() *PipeServer {
	return &PipeServer{cmd: make(chan Command, 1)}
}

func TestHandleGetState(t *testing.T) {
	states := []State{Connected, Disconnected, Error, ConnectionError, Connecting}

	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			srv := newTestServer()
			srv.SetState(state)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/state", nil)
			srv.handleGetState(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type: got %q", got)
			}

			var got State
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode state: %v", err)
			}
			if got != state {
				t.Errorf("state: got %q, want %q", got, state)
			}
		})
	}
}

func TestHandleDisconnect(t *testing.T) {
	srv := newTestServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/disconnect", nil)
	srv.handleDisconnect(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusAccepted)
	}

	select {
	case cmd := <-srv.cmd:
		if _, ok := cmd.(DisconnectCmd); !ok {
			t.Errorf("command: got %T, want DisconnectCmd", cmd)
		}
	default:
		t.Error("expected a DisconnectCmd, got nothing")
	}
}

func TestHandleConnectionReq(t *testing.T) {
	const limit = 64 * 1024
	// Pads a valid request to the given number of bytes
	sizedBody := func(size int) string {
		const prefix, suffix = `{"accessToken":"`, `"}`
		return prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
	}

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantReq    *ConnectionRequest // nil means no command should be queued
	}{
		{
			name:       "valid request",
			body:       `{"accessToken":"token","authHost":"vpn.example.com","useHttps":true,"serverPort":"443"}`,
			wantStatus: http.StatusAccepted,
			wantReq: &ConnectionRequest{
				AccessToken: "token",
				AuthHost:    "vpn.example.com",
				UseHttps:    true,
				ServerPort:  "443",
			},
		},
		{
			name:       "request at the size limit",
			body:       sizedBody(limit),
			wantStatus: http.StatusAccepted,
			wantReq:    &ConnectionRequest{AccessToken: strings.Repeat("a", limit-len(`{"accessToken":""}`))},
		},
		{
			name:       "request over the size limit",
			body:       sizedBody(limit + 1),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid JSON",
			body:       `{"accessToken":`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "wrong field type",
			body:       `{"useHttps":"yes"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty body",
			body:       "",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer()

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/connect", strings.NewReader(tt.body))
			srv.handleConnectionReq(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status: got %d, want %d", rec.Code, tt.wantStatus)
			}

			select {
			case cmd := <-srv.cmd:
				if tt.wantReq == nil {
					t.Fatalf("expected no command, got %T", cmd)
				}
				connect, ok := cmd.(ConnectCmd)
				if !ok {
					t.Fatalf("command: got %T, want ConnectCmd", cmd)
				}
				if connect.Req != *tt.wantReq {
					t.Errorf("request: got %+v, want %+v", connect.Req, *tt.wantReq)
				}
			default:
				if tt.wantReq != nil {
					t.Error("expected a ConnectCmd, got nothing")
				}
			}
		})
	}
}

func TestSendReturnsWhenRequestIsCancelled(t *testing.T) {
	// An unbuffered channel with no reader, so the command can never be delivered
	srv := &PipeServer{cmd: make(chan Command)}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/disconnect", nil).WithContext(ctx)

	// Runs separately so a blocked handler fails the test instead of hanging it
	done := make(chan struct{})
	go func() {
		srv.handleDisconnect(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the request was cancelled")
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

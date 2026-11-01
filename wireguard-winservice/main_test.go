package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/drakovic23/wg-vpn/wireguard-winservice/winpipe"
	"github.com/kardianos/service"
)

var testKeys = Keys{PrivateKey: "test-private-key", PublicKey: "test-public-key"}

type recordedRequest struct {
	method      string
	path        string
	auth        string
	contentType string
	body        []byte
}

// logger is normally set in main, so tests have to provide it.
func useTestLogger(t *testing.T) {
	t.Helper()
	old := logger
	logger = service.ConsoleLogger
	t.Cleanup(func() { logger = old })
}

// requestFor builds a connection request pointing at a local test server.
func requestFor(t *testing.T, server *httptest.Server, useHttps bool) winpipe.ConnectionRequest {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split server address: %v", err)
	}
	return winpipe.ConnectionRequest{
		AccessToken: "test-token",
		AuthHost:    host,
		UseHttps:    useHttps,
		ServerPort:  port,
	}
}

func TestAuthCallSuccess(t *testing.T) {
	useTestLogger(t)

	// Handlers run on another goroutine, so pass the request back over a channel
	received := make(chan recordedRequest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- recordedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		}

		json.NewEncoder(w).Encode(addPeerResponse{
			ServerPublicKey: "server-public-key",
			ListenPort:      51820,
			TunnelIP:        "10.8.0.2",
			AllowedIPs:      "0.0.0.0/0",
		})
	}))
	defer server.Close()

	req := requestFor(t, server, false)

	got, err := authCall(req, testKeys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := deviceConfig{
		PrivateKey: testKeys.PrivateKey,
		PublicKey:  "server-public-key",
		EndPoint:   req.AuthHost + ":51820",
		AllowedIp:  "0.0.0.0/0",
		TunnelIp:   "10.8.0.2",
		KeepAlive:  25,
	}
	if got != want {
		t.Errorf("config: got %+v, want %+v", got, want)
	}

	sent := <-received
	if sent.method != http.MethodPost {
		t.Errorf("method: got %q, want POST", sent.method)
	}
	if sent.path != "/peer/add" {
		t.Errorf("path: got %q, want /peer/add", sent.path)
	}
	if sent.auth != "Bearer test-token" {
		t.Errorf("Authorization: got %q", sent.auth)
	}
	if sent.contentType != "application/json" {
		t.Errorf("Content-Type: got %q", sent.contentType)
	}

	var body addPeerRequest
	if err := json.Unmarshal(sent.body, &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if body.PublicKey != testKeys.PublicKey {
		t.Errorf("public key: got %q, want %q", body.PublicKey, testKeys.PublicKey)
	}
	if strings.Contains(string(sent.body), testKeys.PrivateKey) {
		t.Error("the private key must never be sent to the server")
	}
}

func TestAuthCallFailures(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: "Unauthorized", wantErr: "unexpected status 401: Unauthorized"},
		{name: "server error", status: http.StatusInternalServerError, body: "Error", wantErr: "unexpected status 500: Error"},
		{name: "no content", status: http.StatusNoContent, wantErr: "unexpected status 204"},
		{name: "malformed JSON", status: http.StatusOK, body: "{not json", wantErr: "invalid character"},
		{name: "empty success body", status: http.StatusOK, wantErr: "EOF"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTestLogger(t)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer server.Close()

			got, err := authCall(requestFor(t, server, false), testKeys)

			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected an error containing %q, got: %v", tt.wantErr, err)
			}
			if got != (deviceConfig{}) {
				t.Errorf("expected an empty config, got %+v", got)
			}
		})
	}
}

func TestAuthCallServerUnreachable(t *testing.T) {
	useTestLogger(t)

	server := httptest.NewServer(http.NotFoundHandler())
	req := requestFor(t, server, false)
	server.Close() // nothing is listening on that port now

	if _, err := authCall(req, testKeys); err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestAuthCallRejectsUntrustedCertificate(t *testing.T) {
	useTestLogger(t)

	var called atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0) // the failed handshake is expected
	server.StartTLS()
	defer server.Close()

	// httptest uses a self-signed certificate that the client must not trust
	_, err := authCall(requestFor(t, server, true), testKeys)

	if err == nil {
		t.Fatal("expected a certificate error, got nil")
	}
	if called.Load() {
		t.Error("the access token was sent to a server with an untrusted certificate")
	}
}

func TestAuthCallUsesHttpsWhenRequested(t *testing.T) {
	useTestLogger(t)

	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
	}))
	defer server.Close()

	// The server only speaks plain HTTP, so an HTTPS client cannot reach the handler
	_, err := authCall(requestFor(t, server, true), testKeys)

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if called.Load() {
		t.Error("the request was sent over plain HTTP")
	}
}

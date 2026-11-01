package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc"
	jose "gopkg.in/go-jose/go-jose.v2"
)

const (
	testTenant   = "11111111-1111-1111-1111-111111111111"
	testAudience = "api://test-api"
	testIssuer   = "https://login.microsoftonline.com/" + testTenant + "/v2.0"
	testRole     = "Device.Enroll"
)

// staticKeySet verifies tokens against a local key, so tests never call Microsoft.
type staticKeySet struct {
	key *rsa.PublicKey
}

func (k staticKeySet) VerifySignature(_ context.Context, token string) ([]byte, error) {
	jws, err := jose.ParseSigned(token)
	if err != nil {
		return nil, err
	}
	return jws.Verify(k.key)
}

func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func signToken(t *testing.T, alg jose.SignatureAlgorithm, key any, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, nil)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	token, err := jws.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize token: %v", err)
	}
	return token
}

func validClaims() map[string]any {
	return map[string]any{
		"iss":   testIssuer,
		"aud":   testAudience,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"tid":   testTenant,
		"roles": []string{testRole},
	}
}

func TestValidate(t *testing.T) {
	// validate compares against this global, so restore it when the test ends
	oldTenant := TenantId
	TenantId = testTenant
	t.Cleanup(func() { TenantId = oldTenant })

	trustedKey := newTestKey(t)
	untrustedKey := newTestKey(t)

	verifier := oidc.NewVerifier(testIssuer, staticKeySet{key: &trustedKey.PublicKey}, &oidc.Config{ClientID: testAudience})

	tests := []struct {
		name    string
		edit    func(claims map[string]any)                      // changes the valid claims
		token   func(t *testing.T, claims map[string]any) string // replaces the default signing
		wantErr string                                           // empty means the token must pass
	}{
		{
			name: "valid token",
		},
		{
			name: "valid token with extra roles",
			edit: func(c map[string]any) { c["roles"] = []string{"Other.Role", testRole} },
		},
		{
			name:    "expired token",
			edit:    func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
			wantErr: "token is expired",
		},
		{
			name:    "token not valid yet",
			edit:    func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() },
			wantErr: "before the nbf",
		},
		{
			name:    "wrong audience",
			edit:    func(c map[string]any) { c["aud"] = "api://some-other-api" },
			wantErr: "expected audience",
		},
		{
			name:    "wrong issuer",
			edit:    func(c map[string]any) { c["iss"] = "https://login.microsoftonline.com/other/v2.0" },
			wantErr: "issued by a different provider",
		},
		{
			name:    "wrong tenant",
			edit:    func(c map[string]any) { c["tid"] = "22222222-2222-2222-2222-222222222222" },
			wantErr: "wrong tenant",
		},
		{
			name:    "missing tenant",
			edit:    func(c map[string]any) { delete(c, "tid") },
			wantErr: "wrong tenant",
		},
		{
			name:    "missing roles",
			edit:    func(c map[string]any) { delete(c, "roles") },
			wantErr: "missing role",
		},
		{
			name:    "different role",
			edit:    func(c map[string]any) { c["roles"] = []string{"Other.Role"} },
			wantErr: "missing role",
		},
		{
			name:    "role with different casing",
			edit:    func(c map[string]any) { c["roles"] = []string{"device.enroll"} },
			wantErr: "missing role",
		},
		{
			name: "signed by an untrusted key",
			token: func(t *testing.T, c map[string]any) string {
				return signToken(t, jose.RS256, untrustedKey, c)
			},
			wantErr: "failed to verify signature",
		},
		{
			name: "signed with a disallowed algorithm",
			token: func(t *testing.T, c map[string]any) string {
				return signToken(t, jose.HS256, []byte("a-shared-secret-for-this-test"), c)
			},
			wantErr: "unsupported algorithm",
		},
		{
			name: "tampered payload",
			token: func(t *testing.T, c map[string]any) string {
				parts := strings.Split(signToken(t, jose.RS256, trustedKey, c), ".")
				other := validClaims()
				other["sub"] = "someone-else"
				parts[1] = strings.Split(signToken(t, jose.RS256, trustedKey, other), ".")[1]
				return strings.Join(parts, ".")
			},
			wantErr: "failed to verify signature",
		},
		{
			name:    "malformed token",
			token:   func(*testing.T, map[string]any) string { return "not-a-token" },
			wantErr: "malformed jwt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := validClaims()
			if tt.edit != nil {
				tt.edit(claims)
			}

			var token string
			if tt.token != nil {
				token = tt.token(t, claims)
			} else {
				token = signToken(t, jose.RS256, trustedKey, claims)
			}

			err := validate(context.Background(), verifier, token, testRole)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the token to pass, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected an error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestReadConfig(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantTenant string
		wantClient string
		wantErr    bool
	}{
		{
			name:       "plain values",
			content:    "tenant_id=tenant\napi_app_id=client\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "strips a UTF-8 BOM",
			content:    "\xef\xbb\xbftenant_id=tenant\napi_app_id=client\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "handles Windows line endings",
			content:    "tenant_id=tenant\r\napi_app_id=client\r\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "skips comments and blank lines",
			content:    "# a comment\n\ntenant_id=tenant\n\n# tenant_id=ignored\napi_app_id=client\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "trims double and single quotes",
			content:    "tenant_id=\"tenant\"\napi_app_id='client'\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "trims whitespace around keys and values",
			content:    "  tenant_id  =  tenant  \n\tapi_app_id\t=\tclient\t\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "keys are case insensitive",
			content:    "TENANT_ID=tenant\nApi_App_Id=client\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "keeps an equals sign inside a value",
			content:    "tenant_id=abc=def\napi_app_id=client\n",
			wantTenant: "abc=def",
			wantClient: "client",
		},
		{
			name:       "ignores unknown keys and lines without an equals sign",
			content:    "other=value\nnot a setting\ntenant_id=tenant\napi_app_id=client\n",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:       "works without a trailing newline",
			content:    "tenant_id=tenant\napi_app_id=client",
			wantTenant: "tenant",
			wantClient: "client",
		},
		{
			name:    "missing tenant_id",
			content: "api_app_id=client\n",
			wantErr: true,
		},
		{
			name:    "missing api_app_id",
			content: "tenant_id=tenant\n",
			wantErr: true,
		},
		{
			name:    "empty value",
			content: "tenant_id=\napi_app_id=client\n",
			wantErr: true,
		},
		{
			name:    "value is only quotes",
			content: "tenant_id=\"\"\napi_app_id=client\n",
			wantErr: true,
		},
		{
			name:    "commented out values",
			content: "# tenant_id=tenant\n# api_app_id=client\n",
			wantErr: true,
		},
		{
			name:    "empty file",
			content: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)

			tenant, client, err := readConfig(path)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if !strings.Contains(err.Error(), "must both be set") {
					t.Errorf("unexpected error: %v", err)
				}
				if tenant != "" || client != "" {
					t.Errorf("expected empty values on error, got %q and %q", tenant, client)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tenant != tt.wantTenant {
				t.Errorf("tenant: got %q, want %q", tenant, tt.wantTenant)
			}
			if client != tt.wantClient {
				t.Errorf("client: got %q, want %q", client, tt.wantClient)
			}
		})
	}
}

func TestReadConfigMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.env")

	_, _, err := readConfig(path)

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected a not-exist error, got: %v", err)
	}
}

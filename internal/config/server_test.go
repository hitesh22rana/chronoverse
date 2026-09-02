//nolint:testpackage // Tests unexported configuration validation directly.
package config

import "testing"

func TestValidateServerSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		crypto  string
		csrf    string
		wantErr bool
	}{
		{
			name:    "crypto secret is empty",
			csrf:    "abcdef0123456789abcdef0123456789",
			wantErr: true,
		},
		{
			name:    "csrf secret is empty",
			crypto:  "0123456789abcdef0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "secrets are reused",
			crypto:  "0123456789abcdef0123456789abcdef",
			csrf:    "0123456789abcdef0123456789abcdef",
			wantErr: true,
		},
		{
			name:   "secrets are distinct",
			crypto: "0123456789abcdef0123456789abcdef",
			csrf:   "abcdef0123456789abcdef0123456789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &ServerConfig{
				Crypto: Crypto{Secret: tt.crypto},
				Server: Server{CSRFHMACSecret: tt.csrf},
			}

			err := validateServerSecrets(cfg)
			if tt.wantErr && err == nil {
				t.Fatal("validateServerSecrets() error = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateServerSecrets() error = %v", err)
			}
		})
	}
}

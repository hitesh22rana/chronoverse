//nolint:testpackage // Tests unexported configuration validation directly.
package config

import "testing"

func TestValidateServerSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		environment string
		crypto      string
		csrf        string
		wantErr     bool
	}{
		{
			name:        "development placeholder is now rejected",
			environment: "development",
			crypto:      insecureDefaultSecret,
			csrf:        insecureDefaultSecret,
			wantErr:     true,
		},
		{
			name:        "production secrets are distinct",
			environment: productionEnvironment,
			crypto:      "0123456789abcdef0123456789abcdef",
			csrf:        "abcdef0123456789abcdef0123456789",
		},
		{
			name:        "production crypto secret uses default",
			environment: productionEnvironment,
			crypto:      insecureDefaultSecret,
			csrf:        "abcdef0123456789abcdef0123456789",
			wantErr:     true,
		},
		{
			name:        "production csrf secret uses default",
			environment: productionEnvironment,
			crypto:      "0123456789abcdef0123456789abcdef",
			csrf:        insecureDefaultSecret,
			wantErr:     true,
		},
		{
			name:        "production csrf secret is empty",
			environment: productionEnvironment,
			crypto:      "0123456789abcdef0123456789abcdef",
			csrf:        "",
			wantErr:     true,
		},
		{
			name:        "development crypto is empty",
			environment: "development",
			crypto:      "",
			csrf:        "abcdef0123456789abcdef0123456789",
			wantErr:     true,
		},
		{
			name:        "production secrets are reused",
			environment: productionEnvironment,
			crypto:      "0123456789abcdef0123456789abcdef",
			csrf:        "0123456789abcdef0123456789abcdef",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &ServerConfig{
				Environment: Environment{Env: tt.environment},
				Crypto:      Crypto{Secret: tt.crypto},
				Server:      Server{CSRFHMACSecret: tt.csrf},
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

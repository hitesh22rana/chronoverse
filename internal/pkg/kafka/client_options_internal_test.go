package kafka

import (
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestKafkaTLSOptionsCanRecoverFromRejectedCredentials(t *testing.T) {
	invalid := &config.Kafka{}
	invalid.TLS.Enabled = true
	if client, err := New(t.Context(), WithBrokers("127.0.0.1:1"), WithTLS(invalid)); client != nil || err == nil {
		t.Fatal("constructor accepted invalid TLS credentials")
	}
	cfg := &Config{}
	WithTLS(invalid)(cfg)
	if cfg.tlsErr == nil {
		t.Fatal("invalid TLS option did not retain error")
	}
	cert, key := adapterTLSIdentity(t)
	valid := &config.Kafka{}
	valid.TLS.Enabled = true
	valid.TLS.CertFile = cert
	valid.TLS.KeyFile = key
	valid.TLS.CAFile = cert
	WithTLS(valid)(cfg)
	if cfg.tlsErr != nil || cfg.TLS == nil {
		t.Fatalf("valid TLS did not clear earlier error: %v", cfg.tlsErr)
	}
	client, err := New(t.Context(), WithBrokers("127.0.0.1:1"), WithTLS(valid))
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	WithTLS(&config.Kafka{})(cfg)
	if cfg.tlsErr != nil || cfg.TLS != nil {
		t.Fatal("disabled TLS retained credentials or error")
	}
}

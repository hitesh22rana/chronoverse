package crypto_test

import (
	"encoding/base64"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
)

func TestEncryptDecrypt(t *testing.T) {
	c, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "success",
			data: "data",
			want: "data",
		},
		{
			name: "empty",
			data: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := c.Encrypt(tt.data)
			if err != nil {
				t.Fatal(err)
			}

			decrypted, err := c.Decrypt(encrypted)
			if err != nil {
				t.Fatal(err)
			}

			if decrypted != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, decrypted)
			}
		})
	}
}

func TestEncryptUsesUniqueNonces(t *testing.T) {
	c, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	enc1, err := c.Encrypt("same plaintext")
	if err != nil {
		t.Fatal(err)
	}

	enc2, err := c.Encrypt("same plaintext")
	if err != nil {
		t.Fatal(err)
	}

	if enc1 == enc2 {
		t.Fatal("expected two encryptions of the same plaintext to produce different ciphertexts due to random nonces")
	}
}

func TestDecryptRejectsInvalidData(t *testing.T) {
	c, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data string
	}{
		{
			name: "too short",
			data: base64.StdEncoding.EncodeToString([]byte("too_short")),
		},
		{
			name: "invalid base64",
			data: "invalid-base64!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.Decrypt(tt.data); err == nil {
				t.Fatal("expected decryption to fail")
			}
		})
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	c, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	encrypted, err := c.Encrypt("authenticated data")
	if err != nil {
		t.Fatal(err)
	}

	cipherText, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	cipherText[len(cipherText)-1] ^= 1

	if _, err := c.Decrypt(base64.StdEncoding.EncodeToString(cipherText)); err == nil {
		t.Fatal("expected decryption to reject modified ciphertext")
	}
}

func TestNewRejectsInvalidKeyLengths(t *testing.T) {
	for _, secret := range []string{"", "short", "012345678901234567890123456789012"} {
		c, err := crypto.New(secret)
		if c != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("New(%d bytes) = %v, %v; want nil and InvalidArgument", len(secret), c, err)
		}
	}
}

func TestZeroValueRejectsEncryptionAndDecryption(t *testing.T) {
	var c crypto.Crypto
	encrypted, err := c.Encrypt("private data")
	if encrypted != "" || status.Code(err) != codes.Internal {
		t.Fatalf("Encrypt() = %q, %v; want empty output and Internal", encrypted, err)
	}
	decrypted, err := c.Decrypt("anything")
	if decrypted != "" || status.Code(err) != codes.Internal {
		t.Fatalf("Decrypt() = %q, %v; want empty output and Internal", decrypted, err)
	}
}

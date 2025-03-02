package crypto_test

import (
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
)

func Test(t *testing.T) {
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

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// bytes are random bytes.
var bytes = []byte{35, 46, 57, 24, 85, 35, 24, 74, 87, 35, 88, 98, 66, 32, 14, 0o5}

// Crypto is responsible for encrypting and decrypting data.
type Crypto struct {
	secret string
}

// New creates a new Crypto.
func New(secret string) (*Crypto, error) {
	// AES-256 secret key must be 32 bytes long
	if len(secret) != 32 {
		return nil, status.Error(codes.InvalidArgument, "secret must be 32 bytes long")
	}

	return &Crypto{secret: secret}, nil
}

// encode encodes the data.
func (c *Crypto) encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// decode decodes the string.
func (c *Crypto) decode(s string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to decode data: %v", err)
	}

	return data, nil
}

// Encrypt encrypts the data.
func (c *Crypto) Encrypt(data string) (string, error) {
	block, err := aes.NewCipher([]byte(c.secret))
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to create new cipher: %v", err)
	}

	plainText := []byte(data)
	stream := cipher.NewCTR(block, bytes)
	cipherText := make([]byte, len(plainText))
	stream.XORKeyStream(cipherText, plainText)
	return c.encode(cipherText), nil
}

// Decrypt decrypts the data.
func (c *Crypto) Decrypt(data string) (string, error) {
	block, err := aes.NewCipher([]byte(c.secret))
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to create new cipher: %v", err)
	}

	cipherText, err := c.decode(data)
	if err != nil {
		return "", err
	}

	plainText := make([]byte, len(cipherText))
	cfb := cipher.NewCTR(block, bytes)
	cfb.XORKeyStream(plainText, cipherText)
	return string(plainText), nil
}

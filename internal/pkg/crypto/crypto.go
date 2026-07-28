package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
		return nil, status.Errorf(codes.InvalidArgument, "failed to decode data: %v", err)
	}

	return data, nil
}

// Encrypt encrypts the data.
func (c *Crypto) Encrypt(data string) (string, error) {
	block, err := aes.NewCipher([]byte(c.secret))
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to create new cipher: %v", err)
	}

	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to create authenticated cipher: %v", err)
	}

	// Seal authenticates the ciphertext and prepends a random nonce.
	cipherText := aead.Seal(nil, nil, []byte(data), nil)
	return c.encode(cipherText), nil
}

// Decrypt decrypts the data.
func (c *Crypto) Decrypt(data string) (string, error) {
	block, err := aes.NewCipher([]byte(c.secret))
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to create new cipher: %v", err)
	}

	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to create authenticated cipher: %v", err)
	}

	decodedBytes, err := c.decode(data)
	if err != nil {
		return "", err
	}

	if len(decodedBytes) < aead.Overhead() {
		return "", status.Error(codes.InvalidArgument, "ciphertext is too short")
	}

	plainText, err := aead.Open(nil, nil, decodedBytes, nil)
	if err != nil {
		return "", status.Error(codes.InvalidArgument, "failed to authenticate ciphertext")
	}

	return string(plainText), nil
}

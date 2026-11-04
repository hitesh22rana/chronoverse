package crypto

import "testing"

type invalidBlock struct{}

func (invalidBlock) BlockSize() int      { return 8 }
func (invalidBlock) Encrypt(_, _ []byte) {}
func (invalidBlock) Decrypt(_, _ []byte) {}

func TestNewGCMRejectsInvalidBlockSize(t *testing.T) {
	if _, err := newGCM(invalidBlock{}); err == nil {
		t.Fatal("expected invalid block size to be rejected")
	}
}

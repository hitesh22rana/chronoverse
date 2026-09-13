//nolint:testpackage // Pins the bundled dictionary wiring directly.
package users

import (
	"testing"
)

func TestCommonPasswordsWired(t *testing.T) {
	t.Parallel()

	if len(commonPasswords) == 0 {
		t.Fatal("bundled dictionary is empty")
	}
	for _, password := range []string{"password", "12345678", "qwerty123"} {
		if _, ok := commonPasswords[password]; !ok {
			t.Fatalf("bundled dictionary missing %q", password)
		}
	}
}

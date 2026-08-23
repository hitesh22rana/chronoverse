package commandidempotency

import "testing"

func TestValidateLegacyIdentityWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fresh        bool
		rowsAffected int64
		wantErr      bool
	}{
		{name: "fresh insert", fresh: true, rowsAffected: 1},
		{name: "replay same hash", rowsAffected: 1},
		{name: "canonical replay preserves original lexical hash", rowsAffected: 0},
		{name: "fresh conflicting metadata", fresh: true, rowsAffected: 0, wantErr: true},
		{name: "multiple writes violate invariant", rowsAffected: 2, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateLegacyIdentityWrite(tt.fresh, tt.rowsAffected)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateLegacyIdentityWrite() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

//nolint:testpackage // Tests the unexported interval parser directly.
package server

import "testing"

func TestParseOptionalPositiveInt32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    int32
		wantErr bool
	}{
		{name: "omitted", value: "", want: 0},
		{name: "positive", value: "5", want: 5},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
		{name: "fractional", value: "1.5", wantErr: true},
		{name: "non numeric", value: "five", wantErr: true},
		{name: "overflow", value: "2147483648", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseOptionalPositiveInt32(test.value)
			if test.wantErr && err == nil {
				t.Fatal("expected parse error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got != test.want {
				t.Fatalf("got %d, want %d", got, test.want)
			}
		})
	}
}

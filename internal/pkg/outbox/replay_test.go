package outbox_test

import (
	"testing"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/outbox"
)

func TestReplayIntervalContract(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		base, maximum, wantBase, wantMaximum time.Duration
		invalid                              bool
	}{
		{name: "defaults", wantBase: 5 * time.Minute, wantMaximum: time.Hour},
		{name: "minimum", base: time.Millisecond, maximum: time.Millisecond, wantBase: time.Millisecond, wantMaximum: time.Millisecond},
		{name: "default maximum", base: time.Second, wantBase: time.Second, wantMaximum: time.Hour},
		{name: "default base", maximum: 2 * time.Hour, wantBase: 5 * time.Minute, wantMaximum: 2 * time.Hour},
		{name: "negative base", base: -1, invalid: true},
		{name: "negative maximum", maximum: -1, invalid: true},
		{name: "submillisecond", base: time.Microsecond, invalid: true},
		{name: "fractional base", base: time.Millisecond + 1, invalid: true},
		{name: "fractional maximum", maximum: time.Hour + 1, invalid: true},
		{name: "inverted", base: 2 * time.Hour, maximum: time.Hour, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, maximum, err := outbox.NormalizeReplayIntervals(tc.base, tc.maximum)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid replay timing accepted")
				}
				return
			}
			if err != nil || base != tc.wantBase || maximum != tc.wantMaximum {
				t.Fatalf("got %s/%s/%v want %s/%s", base, maximum, err, tc.wantBase, tc.wantMaximum)
			}
		})
	}
}

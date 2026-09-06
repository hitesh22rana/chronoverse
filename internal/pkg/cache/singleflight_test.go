package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cachepkg "github.com/hitesh22rana/chronoverse/internal/pkg/cache"
)

func TestWaitSingleflightResult(t *testing.T) {
	repoErr := errors.New("repository failed")
	for _, tt := range []struct {
		name   string
		result singleflight.Result
		want   string
		err    error
	}{
		{name: "value", result: singleflight.Result{Val: "shared"}, want: "shared"},
		{name: "error", result: singleflight.Result{Err: repoErr}, err: repoErr},
		{name: "wrong type", result: singleflight.Result{Val: 42}, err: status.Error(codes.Internal, "invalid singleflight result type")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			results := make(chan singleflight.Result, 1)
			results <- tt.result
			got, err := cachepkg.WaitSingleflightResult[string](t.Context(), results)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, err, tt.want, tt.err)
			}
		})
	}

	for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			deadline := time.Now().Add(time.Hour)
			if code == codes.DeadlineExceeded {
				deadline = time.Unix(0, 0)
			}
			ctx, cancel := context.WithDeadline(t.Context(), deadline)
			cancel()
			got, err := cachepkg.WaitSingleflightResult[string](ctx, make(chan singleflight.Result))
			if got != "" || status.Code(err) != code {
				t.Fatalf("got (%q, %v), want empty value and %s", got, err, code)
			}
		})
	}
}

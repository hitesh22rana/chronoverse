//nolint:testpackage // Tests private renewal and wait-error contracts.
package imagepull

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type contractClient struct {
	inspect func(context.Context, string) (bool, error)
	build   func(context.Context, string) error
}

func (c contractClient) ImageExists(ctx context.Context, image string) (bool, error) {
	return c.inspect(ctx, image)
}

func (c contractClient) Build(ctx context.Context, image string) error { return c.build(ctx, image) }

func (contractClient) DockerHost() string { return "tcp://runtime:2375" }

type contractLocks struct {
	acquire func(context.Context, string, time.Duration) (string, bool, error)
	extend  func(context.Context, string, string, time.Duration) (bool, error)
	release func(context.Context, string, string) error
}

func (l contractLocks) AcquireDistributedLockWithToken(ctx context.Context, key string, ttl time.Duration) (token string, acquired bool, err error) {
	return l.acquire(ctx, key, ttl)
}

func (l contractLocks) ExtendDistributedLockWithToken(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	return l.extend(ctx, key, token, ttl)
}

func (l contractLocks) ReleaseDistributedLockWithToken(ctx context.Context, key, token string) error {
	return l.release(ctx, key, token)
}

func TestEnsureInspectionAndAcquisitionFailuresStopBeforeBuild(t *testing.T) {
	for _, stage := range []string{"inspect", "acquire"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage + " unavailable")
			acquired := false
			client := contractClient{
				inspect: func(context.Context, string) (bool, error) {
					if stage == "inspect" {
						return false, failure
					}
					return false, nil
				},
				build: func(context.Context, string) error {
					t.Error("unexpected image build")
					return errors.New("unexpected image build")
				},
			}
			locks := contractLocks{acquire: func(context.Context, string, time.Duration) (string, bool, error) {
				acquired = true
				return "", false, failure
			}}
			if err := Ensure(t.Context(), client, locks, "image", Config{}); !errors.Is(err, failure) {
				t.Fatalf("Ensure() = %v, want original failure %v", err, failure)
			}
			if acquired != (stage == "acquire") {
				t.Fatalf("acquisition attempted = %v, stage = %s", acquired, stage)
			}
		})
	}
}

func TestEnsureDefaultConfigPreservesBuildErrorDespiteReleaseFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		buildFailure := errors.New("registry denied image")
		start := time.Now()
		key := lockKey("tcp://runtime:2375", "image")
		buildCalls, releases := 0, 0
		client := contractClient{
			inspect: func(context.Context, string) (bool, error) { return false, nil },
			build: func(_ context.Context, image string) error {
				buildCalls++
				if image != "image" {
					t.Errorf("build image = %q", image)
				}
				return buildFailure
			},
		}
		locks := contractLocks{
			acquire: func(ctx context.Context, gotKey string, ttl time.Duration) (string, bool, error) {
				deadline, ok := ctx.Deadline()
				if !ok || deadline.Sub(start) != defaultLockWaitTimeout || ttl != defaultLockTTL || gotKey != key {
					t.Errorf("default acquisition: deadline=%v, ttl=%v, key=%q", deadline, ttl, gotKey)
				}
				return "owner", true, nil
			},
			release: func(ctx context.Context, gotKey, token string) error {
				releases++
				if ctx.Err() != nil || gotKey != key || token != "owner" {
					t.Errorf("invalid release context or ownership")
				}
				return errors.New("release connection closed")
			},
		}
		if err := Ensure(t.Context(), client, locks, "image", Config{TTL: -1, WaitTimeout: -1, RetryInterval: -1}); !errors.Is(err, buildFailure) {
			t.Fatalf("Ensure() = %v, want build failure", err)
		}
		if buildCalls != 1 || releases != 1 {
			t.Fatalf("builds=%d releases=%d, want1 each", buildCalls, releases)
		}
	})
}

func TestEnsureRenewalFailureCancelsAndJoinsBuildBeforeReleasingOwnership(t *testing.T) {
	for _, renewal := range []string{"lost", "error"} {
		t.Run(renewal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				failure := errors.New("renewal datastore unavailable")
				buildStopped := false
				var released, extensions int
				key := lockKey("runtime-id", "image")
				client := contractClient{
					inspect: func(context.Context, string) (bool, error) { return false, nil },
					build: func(ctx context.Context, _ string) error {
						<-ctx.Done()
						buildStopped = true
						return ctx.Err()
					},
				}
				locks := contractLocks{
					acquire: func(context.Context, string, time.Duration) (string, bool, error) { return "owner", true, nil },
					extend: func(ctx context.Context, gotKey, token string, ttl time.Duration) (bool, error) {
						extensions++
						assertLockOwnership(ctx, t, gotKey, token, key)
						if ttl != time.Millisecond {
							t.Errorf("invalid renewal ownership/context/TTL")
						}
						if renewal == "error" {
							return false, failure
						}
						return false, nil
					},
					release: func(ctx context.Context, gotKey, token string) error {
						released++
						assertLockOwnership(ctx, t, gotKey, token, key)
						if !buildStopped {
							t.Errorf("release preceded build join or lost ownership/context")
						}
						return nil
					},
				}
				start := time.Now()
				err := Ensure(t.Context(), client, locks, "image", Config{TTL: time.Millisecond, LockScope: "runtime-id"})
				if renewal == "error" && !errors.Is(err, failure) {
					t.Fatalf("Ensure() = %v, want renewal failure", err)
				}
				if renewal == "lost" && status.Code(err) != codes.ResourceExhausted {
					t.Fatalf("Ensure() = %v, want ownership loss", err)
				}
				if elapsed := time.Since(start); elapsed != minLockRenewInterval {
					t.Errorf("renewal elapsed=%v, want minimum interval", elapsed)
				}
				if extensions != 1 || released != 1 || !buildStopped {
					t.Fatalf("extensions=%d releases=%d buildStopped=%v", extensions, released, buildStopped)
				}
			})
		})
	}
}

func TestEnsureSuccessfulRenewalUntilCallerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		extensions, released := 0, 0
		client := contractClient{
			inspect: func(context.Context, string) (bool, error) { return false, nil },
			build:   func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() },
		}
		locks := contractLocks{
			acquire: func(context.Context, string, time.Duration) (string, bool, error) { return "owner", true, nil },
			extend:  func(context.Context, string, string, time.Duration) (bool, error) { extensions++; return true, nil },
			release: func(releaseCtx context.Context, _, token string) error {
				released++
				if releaseCtx.Err() != nil || token != "owner" {
					t.Errorf("release must retain ownership and ignore caller cancellation")
				}
				return nil
			},
		}
		if err := Ensure(ctx, client, locks, "image", Config{TTL: 6 * time.Second}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Ensure() = %v", err)
		}
		synctest.Wait()
		if extensions != 2 || released != 1 {
			t.Fatalf("extensions=%d releases=%d, want2 and1", extensions, released)
		}
	})
}

func TestEnsureHeldLockRespectsCallerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		locks := contractLocks{acquire: func(context.Context, string, time.Duration) (string, bool, error) {
			cancel()
			return "", false, nil
		}}
		client := contractClient{inspect: func(context.Context, string) (bool, error) { return false, nil }}
		if err := Ensure(ctx, client, locks, "image", Config{}); status.Code(err) != codes.Canceled {
			t.Fatalf("Ensure() = %v, want canceled", err)
		}
	})
}

func TestWaitErrorPreservesCancellationAndRetryClassification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		deadline, stop := context.WithTimeout(t.Context(), time.Second)
		defer stop()
		<-deadline.Done()
		other := errors.New("unexpected transport failure")
		for _, tt := range []struct {
			name string
			ctx  func() context.Context
			err  error
			code codes.Code
		}{
			{"parent canceled", func() context.Context { return canceled }, context.DeadlineExceeded, codes.Canceled},
			{"parent deadline", func() context.Context { return deadline }, context.DeadlineExceeded, codes.DeadlineExceeded},
			{"wait expired", t.Context, context.DeadlineExceeded, codes.ResourceExhausted},
			{"wait canceled", t.Context, context.Canceled, codes.Canceled},
			{"other failure", t.Context, other, codes.Unknown},
		} {
			err := waitError(tt.ctx(), tt.err)
			if status.Code(err) != tt.code {
				t.Fatalf("%s: waitError() = %v, want%s", tt.name, err, tt.code)
			}
			if errors.Is(tt.err, other) && !errors.Is(err, other) {
				t.Fatal("original failure was replaced")
			}
		}
	})
}

func assertLockOwnership(ctx context.Context, t *testing.T, key, token, wantKey string) {
	t.Helper()
	if ctx.Err() != nil || key != wantKey || token != "owner" {
		t.Errorf("invalid lock ownership: key=%q token=%q context=%v", key, token, ctx.Err())
	}
}

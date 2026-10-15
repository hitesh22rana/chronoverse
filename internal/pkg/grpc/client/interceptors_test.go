package client //nolint:testpackage // Tests the private interceptor policy directly.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/eapache/go-resiliency/breaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type timeoutError struct{ timeout bool }

func (e timeoutError) Error() string   { return "network failure" }
func (e timeoutError) Timeout() bool   { return e.timeout }
func (e timeoutError) Temporary() bool { return false }

func TestIsCircuitBreakerError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"success", nil, false},
		{"ordinary", errors.New("ordinary failure"), false},
		{"canceled", context.Canceled, true},
		{"wrapped canceled", fmt.Errorf("call: %w", context.Canceled), true},
		{"deadline", context.DeadlineExceeded, true},
		{"wrapped deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), true},
		{"network timeout", timeoutError{true}, true},
		{"wrapped network timeout", fmt.Errorf("dial: %w", timeoutError{true}), true},
		{"network non-timeout", timeoutError{false}, false},
		{"wrapped network non-timeout", &net.OpError{Op: "dial", Net: "tcp", Err: timeoutError{false}}, false},
	}
	counted := map[codes.Code]bool{
		codes.Canceled: true, codes.DeadlineExceeded: true, codes.ResourceExhausted: true,
		codes.Aborted: true, codes.Unimplemented: true, codes.Internal: true,
		codes.Unavailable: true, codes.DataLoss: true,
	}
	for code := codes.OK; code <= codes.Unauthenticated; code++ {
		err := status.Error(code, "RPC failure")
		tests = append(tests, struct {
			name string
			err  error
			want bool
		}{code.String(), err, counted[code]})
		if err != nil {
			tests = append(tests, struct {
				name string
				err  error
				want bool
			}{"wrapped " + code.String(), fmt.Errorf("RPC: %w", err), counted[code]})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCircuitBreakerError(tt.err); got != tt.want {
				t.Fatalf("isCircuitBreakerError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestCircuitBreakerInterceptors(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"unary", "stream"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name    string
				err     error
				counted bool
			}{
				{"success", nil, false},
				{"application error", status.Error(codes.InvalidArgument, "invalid request"), false},
				{"ordinary error", errors.New("application failure"), false},
				{"server error", status.Error(codes.Unavailable, "server unavailable"), true},
				{"cancellation", context.Canceled, true},
				{"deadline", context.DeadlineExceeded, true},
				{"network timeout", fmt.Errorf("dial: %w", timeoutError{true}), true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					cb := breaker.New(1, 1, time.Hour)
					returnedStream := &interceptorTestStream{}
					calls := 0
					invoke := interceptorInvocation(t, kind, cb, returnedStream, tt.err, &calls)
					stream, err := invoke()
					if !sameError(err, tt.err) {
						t.Fatalf("error = %v, want original %v", err, tt.err)
					}
					if kind == "stream" && stream != returnedStream {
						t.Fatal("stream identity changed")
					}
					if calls != 1 {
						t.Fatalf("calls = %d, want 1", calls)
					}
					stream, err = invoke()
					if tt.counted {
						if !sameError(err, breaker.ErrBreakerOpen) || stream != nil || calls != 1 {
							t.Fatalf("open breaker: stream=%v error=%v calls=%d", stream, err, calls)
						}
					} else {
						if !sameError(err, tt.err) || calls != 2 {
							t.Fatalf("ignored failure: error=%v calls=%d", err, calls)
						}
						if kind == "stream" && stream != returnedStream {
							t.Fatal("stream identity changed on second invocation")
						}
					}
				})
			}
		})
	}
}

func sameError(got, want error) bool {
	return got == want //nolint:errorlint // Exact identity is the interceptor contract.
}

func interceptorInvocation(t *testing.T, kind string, cb *breaker.Breaker, stream grpc.ClientStream, rpcErr error, calls *int) func() (grpc.ClientStream, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn := new(grpc.ClientConn)
	options := []grpc.CallOption{grpc.WaitForReady(true), grpc.MaxCallRecvMsgSize(1024)}
	req, reply := new(int), new(int)
	desc := &grpc.StreamDesc{ServerStreams: true}
	if kind == "unary" {
		invoker := func(gotCtx context.Context, method string, gotReq, gotReply any, gotConn *grpc.ClientConn, opts ...grpc.CallOption) error {
			*calls++
			if gotCtx != ctx || method != "/service/method" || gotReq != req || gotReply != reply || gotConn != conn || !slices.Equal(opts, options) {
				t.Fatal("unary invocation arguments changed")
			}
			return rpcErr
		}
		interceptor := circuitBreakerUnaryInterceptor(cb)
		return func() (grpc.ClientStream, error) {
			return nil, interceptor(ctx, "/service/method", req, reply, conn, invoker, options...)
		}
	}
	streamer := func(gotCtx context.Context, gotDesc *grpc.StreamDesc, gotConn *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		*calls++
		if gotCtx != ctx || gotDesc != desc || gotConn != conn || method != "/service/method" || !slices.Equal(opts, options) {
			t.Fatal("stream invocation arguments changed")
		}
		return stream, rpcErr
	}
	interceptor := circuitBreakerStreamInterceptor(cb)
	return func() (grpc.ClientStream, error) {
		return interceptor(ctx, desc, conn, "/service/method", streamer, options...)
	}
}

type interceptorTestStream struct{ grpc.ClientStream }

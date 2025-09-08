package client

import (
	"context"
	"errors"
	"net"

	"github.com/eapache/go-resiliency/breaker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func circuitBreakerUnaryInterceptor(cb *breaker.Breaker) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var rpcErr error

		cbErr := cb.Run(func() error {
			rpcErr = invoker(ctx, method, req, reply, cc, opts...)
			if isCircuitBreakerError(rpcErr) {
				return rpcErr
			}
			return nil
		})

		// If breaker returned an error, prefer the RPC error if it exists.
		if cbErr != nil {
			if rpcErr != nil {
				return rpcErr
			}
			return cbErr
		}

		return rpcErr
	}
}

func circuitBreakerStreamInterceptor(cb *breaker.Breaker) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		var stream grpc.ClientStream
		var streamErr error
		cbErr := cb.Run(func() error {
			stream, streamErr = streamer(ctx, desc, cc, method, opts...)
			if isCircuitBreakerError(streamErr) {
				return streamErr
			}
			return nil
		})

		if cbErr != nil {
			// If the underlying RPC returned an error that we treat as a circuit-breaker error,
			// return that error (breaker counted it). If cbErr is non-nil but the RPC error
			// was nil, it's likely the breaker is open - return the breaker error.
			if streamErr != nil {
				return stream, streamErr
			}
			return stream, cbErr
		}

		// cbErr == nil. If the RPC returned a non-circuit-breaker error, return it to caller
		// but it won't be counted by the breaker.
		return stream, streamErr
	}
}

// isCircuitBreakerError determines whether an error should be counted against the circuit
// breaker. Only treat internal server errors, deadline/timeouts and network timeouts as
// circuit-breaker errors.
func isCircuitBreakerError(err error) bool {
	if err == nil {
		return false
	}

	// Direct context deadline
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	// gRPC status codes
	//nolint:exhaustive // Only treating some codes as circuit-breaker errors
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.DeadlineExceeded,
			codes.Canceled,
			codes.ResourceExhausted,
			codes.Aborted,
			codes.Unimplemented,
			codes.Internal,
			codes.Unavailable,
			codes.DataLoss:
			return true
		default:
			return false
		}
	}

	// Network-level timeouts (net.Error)
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

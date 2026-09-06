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
			// Prefer the RPC error when counted; otherwise the breaker is open.
			if streamErr != nil {
				return stream, streamErr
			}
			return stream, cbErr
		}

		// Uncounted RPC error; return as-is.
		return stream, streamErr
	}
}

// isCircuitBreakerError reports whether err should count against the circuit.
// Only internal, deadline/timeout, and network-timeout failures count.
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

package otel

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/stats"
)

// HTTPHandler instruments an HTTP server handler with the configured global
// OpenTelemetry providers. Route templates from http.ServeMux are used for
// span names and metric attributes to avoid recording high-cardinality paths.
func HTTPHandler(handler http.Handler, operation string, opts ...otelhttp.Option) http.Handler {
	defaultOpts := make([]otelhttp.Option, 0, 3+len(opts))
	defaultOpts = append(defaultOpts,
		otelhttp.WithTracerProvider(otel.GetTracerProvider()),
		otelhttp.WithMeterProvider(otel.GetMeterProvider()),
		otelhttp.WithSpanNameFormatter(func(operation string, request *http.Request) string {
			if state, ok := request.Context().Value(httpRouteStateKey{}).(*httpRouteState); ok && state.route != "" {
				return request.Method + " " + state.route
			}
			if request.Pattern == "" {
				return operation
			}

			return request.Method + " " + request.Pattern
		}),
	)

	instrumented := otelhttp.NewHandler(withHTTPRouteAttributes(handler), operation, append(defaultOpts, opts...)...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &httpRouteState{}
		ctx := context.WithValue(r.Context(), httpRouteStateKey{}, state)
		instrumented.ServeHTTP(w, r.WithContext(ctx))
	})
}

type httpRouteStateKey struct{}

type httpRouteState struct {
	route string
}

func withHTTPRouteAttributes(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)

		route := httpRoute(r.Pattern)
		if route == "" {
			return
		}

		attr := semconv.HTTPRoute(route)
		if state, ok := r.Context().Value(httpRouteStateKey{}).(*httpRouteState); ok {
			state.route = route
		}
		labeler, _ := otelhttp.LabelerFromContext(r.Context())
		labeler.Add(attr)
		trace.SpanFromContext(r.Context()).SetAttributes(attr)
	})
}

func httpRoute(pattern string) string {
	if index := strings.IndexByte(pattern, '/'); index >= 0 {
		return pattern[index:]
	}

	return ""
}

// HTTPTransport instruments outbound HTTP requests with the configured global
// OpenTelemetry providers.
func HTTPTransport(transport http.RoundTripper, opts ...otelhttp.Option) http.RoundTripper {
	if transport == nil {
		transport = http.DefaultTransport
	}

	defaultOpts := make([]otelhttp.Option, 0, 2+len(opts))
	defaultOpts = append(defaultOpts,
		otelhttp.WithTracerProvider(otel.GetTracerProvider()),
		otelhttp.WithMeterProvider(otel.GetMeterProvider()),
	)

	return otelhttp.NewTransport(transport, append(defaultOpts, opts...)...)
}

// GRPCServerHandler instruments inbound gRPC calls with the configured global
// OpenTelemetry providers.
func GRPCServerHandler(opts ...otelgrpc.Option) stats.Handler {
	defaultOpts := make([]otelgrpc.Option, 0, 2+len(opts))
	defaultOpts = append(defaultOpts,
		otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
		otelgrpc.WithMeterProvider(otel.GetMeterProvider()),
	)

	return otelgrpc.NewServerHandler(append(defaultOpts, opts...)...)
}

// GRPCClientHandler instruments outbound gRPC calls with the configured global
// OpenTelemetry providers.
func GRPCClientHandler(opts ...otelgrpc.Option) stats.Handler {
	defaultOpts := make([]otelgrpc.Option, 0, 2+len(opts))
	defaultOpts = append(defaultOpts,
		otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
		otelgrpc.WithMeterProvider(otel.GetMeterProvider()),
	)

	return otelgrpc.NewClientHandler(append(defaultOpts, opts...)...)
}

// Meter returns a meter from the configured global OpenTelemetry provider.
func Meter(scope string, opts ...metric.MeterOption) metric.Meter {
	return otel.GetMeterProvider().Meter(scope, opts...)
}

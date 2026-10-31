// Tests private provider ownership without exposing instrumentation hooks.
package otel

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	apiotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Embed a real SDK reader: instrumentation registration and shutdown still use
// the actual SDK, while the wrapper records the reader ownership boundary.
type shutdownContextKey struct{}

type shutdownReader struct {
	sdkmetric.Reader
	calls        int
	contextErr   error
	deadline     time.Time
	hasDeadline  bool
	contextValue any
	shutdownErr  error
}

func (r *shutdownReader) Shutdown(ctx context.Context) error {
	r.calls++
	r.contextErr = ctx.Err()
	r.deadline, r.hasDeadline = ctx.Deadline()
	r.contextValue = ctx.Value(shutdownContextKey{})
	return errors.Join(r.Reader.Shutdown(ctx), r.shutdownErr)
}

type meterStartupFixture struct {
	previous   metric.MeterProvider
	manual     *sdkmetric.ManualReader
	reader     *shutdownReader
	mp         *sdkmetric.MeterProvider
	stage      string
	calls      []string
	cleanupErr error
}

func newMeterStartupFixture(t *testing.T, stage string) *meterStartupFixture {
	t.Helper()
	f := &meterStartupFixture{previous: apiotel.GetMeterProvider(), stage: stage, cleanupErr: errors.New("reader close failed")}
	t.Cleanup(func() {
		if apiotel.GetMeterProvider() != f.previous {
			apiotel.SetMeterProvider(f.previous)
		}
	})
	f.manual = sdkmetric.NewManualReader()
	f.reader = &shutdownReader{Reader: f.manual}
	if stage == "cleanup" {
		f.reader.shutdownErr = f.cleanupErr
	}
	f.mp = sdkmetric.NewMeterProvider(sdkmetric.WithReader(f.reader))
	t.Cleanup(func() {
		if err := f.mp.Shutdown(context.Background()); err != nil && !errors.Is(err, sdkmetric.ErrReaderShutdown) {
			t.Errorf("provider cleanup: %v", err)
		}
	})
	return f
}

func (f *meterStartupFixture) starter(t *testing.T, name string) func(*sdkmetric.MeterProvider) error {
	t.Helper()
	return func(got *sdkmetric.MeterProvider) error {
		f.calls = append(f.calls, name)
		if got != f.mp {
			t.Fatal("instrumentation received a different provider")
		}
		if apiotel.GetMeterProvider() != f.previous {
			t.Fatal("provider published before instrumentation completed")
		}
		counter, err := got.Meter(name).Int64Counter("startup.count")
		if err != nil {
			t.Fatal(err)
		}
		counter.Add(context.Background(), 1)
		if f.stage == name || (f.stage == "cleanup" && name == "runtime") {
			return errors.New("registration failed")
		}
		return nil
	}
}

func (f *meterStartupFixture) assertSuccess(t *testing.T, got *sdkmetric.MeterProvider, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got != f.mp || apiotel.GetMeterProvider() != f.mp || f.reader.calls != 0 {
		t.Fatal("successful provider not transferred to caller")
	}
	var data metricdata.ResourceMetrics
	if collectErr := f.manual.Collect(context.Background(), &data); collectErr != nil {
		t.Fatal(collectErr)
	}
	if len(data.ScopeMetrics) != 2 {
		t.Fatalf("registered metrics missing: %#v", data.ScopeMetrics)
	}
}

func (f *meterStartupFixture) assertRegistrationError(t *testing.T, err error) {
	t.Helper()
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "registration failed") {
		t.Fatalf("lost registration failure: %v", err)
	}
	wantStage := "runtime"
	if f.stage == "host" {
		wantStage = "hostmetrics"
	}
	if !strings.Contains(err.Error(), "failed to start "+wantStage) {
		t.Fatalf("lost failing instrumentation stage: %v", err)
	}
	if f.stage == "cleanup" && !errors.Is(err, f.cleanupErr) {
		t.Fatalf("lost cleanup failure: %v", err)
	}
}

func (f *meterStartupFixture) assertRollback(t *testing.T, got *sdkmetric.MeterProvider, started time.Time) {
	t.Helper()
	if got != nil || apiotel.GetMeterProvider() != f.previous {
		t.Fatal("failed provider escaped or replaced global provider")
	}
	if f.reader.calls != 1 || f.reader.contextErr != nil {
		t.Fatalf("shutdown calls=%d context error=%v", f.reader.calls, f.reader.contextErr)
	}
	if !f.reader.hasDeadline || f.reader.deadline.Before(started.Add(9*time.Second)) || f.reader.deadline.After(time.Now().Add(10*time.Second)) {
		t.Fatalf("cleanup deadline = %v (present %v), want a fresh bounded ten seconds", f.reader.deadline, f.reader.hasDeadline)
	}
	if f.reader.contextValue != f.stage {
		t.Fatalf("cleanup lost parent value: %v", f.reader.contextValue)
	}
	var data metricdata.ResourceMetrics
	if err := f.manual.Collect(context.Background(), &data); !errors.Is(err, sdkmetric.ErrReaderShutdown) {
		t.Fatalf("reader still active: %v", err)
	}
	if err := f.mp.Shutdown(context.Background()); !errors.Is(err, sdkmetric.ErrReaderShutdown) {
		t.Fatalf("repeated shutdown: %v", err)
	}
	if f.reader.calls != 1 {
		t.Fatalf("reader closed again: %d", f.reader.calls)
	}
}

func TestRegisterMeterProviderOwnership(t *testing.T) {
	for _, stage := range []string{"success", "host", "runtime", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			f := newMeterStartupFixture(t, stage)
			ctx, cancel := context.WithDeadline(context.WithValue(t.Context(), shutdownContextKey{}, stage), time.Now().Add(-time.Second))
			cancel()
			started := time.Now()
			got, err := registerMeterProvider(ctx, f.mp, f.starter(t, "host"), f.starter(t, "runtime"))
			wantCalls := []string{"host", "runtime"}
			if stage == "host" {
				wantCalls = []string{"host"}
			}
			if !reflect.DeepEqual(f.calls, wantCalls) {
				t.Fatalf("registration order: got %v, want %v", f.calls, wantCalls)
			}
			if stage == "success" {
				f.assertSuccess(t, got, err)
				return
			}
			f.assertRegistrationError(t, err)
			f.assertRollback(t, got, started)
		})
	}
}

func TestInitResourceAttributesAndDetectorFailure(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=test")
	t.Setenv("OTEL_SERVICE_NAME", "environment-name")
	res, err := InitResource(t.Context(), "explicit-name", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	attrs := res.Set()
	for key, want := range map[string]string{"service.name": "explicit-name", "service.version": "v1.2.3", "deployment.environment.name": "test"} {
		got, ok := attrs.Value(attribute.Key(key))
		if !ok || got.AsString() != want {
			t.Fatalf("%s = %v, want %s", key, got, want)
		}
	}
	got, ok := attrs.Value("host.name")
	if !ok || got.AsString() == "" {
		t.Fatal("host identity missing")
	}
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "invalid-attribute-without-equals")
	res, err = InitResource(t.Context(), "service", "version")
	if res != nil || status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "failed to create resource") {
		t.Fatalf("detector failure: resource=%v error=%v", res, err)
	}
}

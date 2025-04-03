package svc

import (
	"context"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	otelpkg "github.com/hitesh22rana/chronoverse/internal/pkg/otel"
)

var (
	// version is the service version.
	version string

	// name is the name of the service.
	name string

	// authPrivateKeyPath is the path to the private key.
	authPrivateKeyPath string

	// authPublicKeyPath is the path to the public key.
	authPublicKeyPath string
)

// Svc contains the service information.
type Svc struct {
	// version is the service version.
	version string

	// name is the name of the service.
	name string

	// authPrivateKeyPath is the path to the private key.
	authPrivateKeyPath string

	// authPublicKeyPath is the path to the public key.
	authPublicKeyPath string
}

// Svc represents the service.
var svc Svc

// GetVersion returns the service version.
func (s Svc) GetVersion() string {
	return s.version
}

// GetName returns the service name.
func (s Svc) GetName() string {
	return s.name
}

// GetAuthPrivateKeyPath returns the path to the private key.
func (s Svc) GetAuthPrivateKeyPath() string {
	return s.authPrivateKeyPath
}

// GetAuthPublicKeyPath returns the path to the public key.
func (s Svc) GetAuthPublicKeyPath() string {
	return s.authPublicKeyPath
}

// setVersion sets the service version.
func setVersion(version string) {
	if svc.version != "" {
		return
	}
	svc.version = version
}

// setName sets the service name.
func setName(name string) {
	if svc.name != "" {
		return
	}
	svc.name = name
}

// setAuthPrivateKeyPath sets the path to the private key.
func setAuthPrivateKeyPath(path string) {
	if svc.authPrivateKeyPath != "" {
		return
	}
	svc.authPrivateKeyPath = path
}

// setAuthPublicKeyPath sets the path to the public key.
func setAuthPublicKeyPath(path string) {
	if svc.authPublicKeyPath != "" {
		return
	}
	svc.authPublicKeyPath = path
}

// Info returns the service information.
func Info() Svc {
	return svc
}

// initSvcInfo initializes the service information.
func initSvcInfo() {
	setVersion(version)
	setName(name)
	setAuthPrivateKeyPath(authPrivateKeyPath)
	setAuthPublicKeyPath(authPublicKeyPath)
}

// Init initializes the service, with all the necessary components.
//
//nolint:gocritic // Ignore the linter for this function
func Init() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize the service information
	initSvcInfo()

	// Initialize the OpenTelemetry Resource
	res, err := otelpkg.InitResource(ctx, svc.GetName(), svc.GetVersion())
	if err != nil {
		panic(err)
	}

	shutdownFuncs := []func() error{}

	// Initialize the OpenTelemetry TracerProvider
	tp, err := otelpkg.InitTracerProvider(ctx, res)
	if err != nil {
		panic(err)
	}
	shutdownFuncs = append(shutdownFuncs, func() error {
		return tp.Shutdown(ctx)
	})

	// Initialize the OpenTelemetry MeterProvider
	mp, err := otelpkg.InitMeterProvider(ctx, res)
	if err != nil {
		panic(err)
	}
	shutdownFuncs = append(shutdownFuncs, func() error {
		return mp.Shutdown(ctx)
	})

	// Initialize the OpenTelemetry LoggerProvider
	lp, err := otelpkg.InitLogProvider(ctx, res)
	if err != nil {
		panic(err)
	}
	shutdownFuncs = append(shutdownFuncs, func() error {
		return lp.Shutdown(ctx)
	})

	// Initialize and set the logger in the context
	ctx, logger := loggerpkg.Init(ctx, svc.GetName(), lp)
	shutdownFuncs = append(shutdownFuncs, func() error {
		return logger.Sync()
	})

	return ctx, func() {
		for _, shutdownFunc := range shutdownFuncs {
			//nolint:errcheck // Ignore errors from shutdown functions
			_ = shutdownFunc()
		}
		cancel()
	}
}

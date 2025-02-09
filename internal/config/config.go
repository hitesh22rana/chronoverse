package config

const envPrefix = ""

// Configuration holds the application configuration.
type Configuration struct {
	Environment
	Otel
}

// Environment holds the environment configuration.
type Environment struct {
	Env string `envconfig:"ENV" default:"development"`
}

// Otel holds the OpenTelemetry configuration.
type Otel struct {
	ExporterOtlpEndpoint string `envconfig:"OTEL_EXPORTER_OTLP_ENDPOINT" default:"http://jaeger:4317"`
}

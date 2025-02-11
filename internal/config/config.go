package config

const envPrefix = ""

// Configuration holds the application configuration.
type Configuration struct {
	Environment
}

// Environment holds the environment configuration.
type Environment struct {
	Env string `envconfig:"ENV" default:"development"`
}

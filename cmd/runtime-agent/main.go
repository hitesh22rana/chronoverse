package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	_ "github.com/KimMachineGun/automemlimit"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/config"
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	runtimerepo "github.com/hitesh22rana/chronoverse/internal/repository/runtime"
)

const (
	// ExitOk and ExitError are the exit codes.
	ExitOk = iota
	// ExitError is the exit code for errors.
	ExitError
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := svcpkg.Init()
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	cfg, err := config.InitRuntimeAgentConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	if cfg.RuntimeAgentConfig.ID == "" || cfg.RuntimeAgentConfig.NodeName == "" || cfg.RuntimeAgentConfig.DockerEndpoint == "" {
		fmt.Fprintln(os.Stderr, "runtime agent id, node name, and docker endpoint are required")
		return ExitError
	}

	pdb, err := postgres.New(ctx, &postgres.Config{
		Host:        cfg.Postgres.Host,
		Port:        cfg.Postgres.Port,
		User:        cfg.Postgres.User,
		Password:    cfg.Postgres.Password,
		Database:    cfg.Postgres.Database,
		MaxConns:    cfg.Postgres.MaxConns,
		MinConns:    cfg.Postgres.MinConns,
		MaxConnLife: cfg.Postgres.MaxConnLife,
		MaxConnIdle: cfg.Postgres.MaxConnIdle,
		DialTimeout: cfg.Postgres.DialTimeout,
		TLSConfig: &postgres.TLSConfig{
			Enabled:  cfg.Postgres.TLS.Enabled,
			CAFile:   cfg.Postgres.TLS.CAFile,
			CertFile: cfg.Postgres.TLS.CertFile,
			KeyFile:  cfg.Postgres.TLS.KeyFile,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer pdb.Close()

	if _, err := container.NewDockerWorkflow(container.WithDockerHost(cfg.RuntimeAgentConfig.DockerEndpoint)); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ping runtime Docker endpoint: %v\n", err)
		return ExitError
	}

	repo := runtimerepo.New(runtimerepo.Config{
		ID:             cfg.RuntimeAgentConfig.ID,
		NodeName:       cfg.RuntimeAgentConfig.NodeName,
		DockerEndpoint: cfg.RuntimeAgentConfig.DockerEndpoint,
		MaxConcurrency: cfg.RuntimeAgentConfig.MaxConcurrency,
	}, pdb)

	loggerpkg.FromContext(ctx).Info(
		"starting runtime agent",
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.String("environment", cfg.Environment.Env),
		zap.String("runtime_id", cfg.RuntimeAgentConfig.ID),
		zap.String("node_name", cfg.RuntimeAgentConfig.NodeName),
		zap.String("docker_endpoint", cfg.RuntimeAgentConfig.DockerEndpoint),
		zap.Duration("heartbeat_interval", cfg.RuntimeAgentConfig.HeartbeatInterval),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		zap.Int64("gomemlimit", debug.SetMemoryLimit(0)),
	)

	if err := repo.RunHeartbeats(ctx, cfg.RuntimeAgentConfig.HeartbeatInterval); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer drainCancel()
	if err := repo.MarkDraining(drainCtx); err != nil {
		loggerpkg.FromContext(ctx).Warn("failed to mark runtime node draining", zap.Error(err))
	}

	return ExitOk
}

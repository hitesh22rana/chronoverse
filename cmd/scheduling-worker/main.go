package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	_ "github.com/KimMachineGun/automemlimit"
	_ "go.uber.org/automaxprocs"
	"go.uber.org/zap"

	"github.com/hitesh22rana/chronoverse/internal/app/scheduler"
	"github.com/hitesh22rana/chronoverse/internal/config"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
	schedulerrepo "github.com/hitesh22rana/chronoverse/internal/repository/scheduler"
	schedulersvc "github.com/hitesh22rana/chronoverse/internal/service/scheduler"
)

const (
	ExitOk = iota
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

	cfg, err := config.InitSchedulingJobConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	pdb, err := postgres.New(ctx, cfg.Postgres.ClientConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	defer pdb.Close()

	repo := schedulerrepo.New(&schedulerrepo.Config{
		BatchSize: cfg.SchedulingWorkerConfig.BatchSize,
	}, pdb)
	svc := schedulersvc.New(repo)
	app := scheduler.New(ctx, &scheduler.Config{
		PollInterval:   cfg.SchedulingWorkerConfig.PollInterval,
		ContextTimeout: cfg.SchedulingWorkerConfig.ContextTimeout,
	}, svc)

	loggerpkg.FromContext(ctx).Info(
		"starting job",
		zap.Any("ctx", ctx),
		zap.String("name", svcpkg.Info().GetName()),
		zap.String("version", svcpkg.Info().GetVersion()),
		zap.String("environment", cfg.Environment.Env),
		zap.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		zap.Int64("gomemlimit", debug.SetMemoryLimit(0)),
	)

	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}

	return ExitOk
}

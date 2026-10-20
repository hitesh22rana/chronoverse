package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartRuntimeHeartbeatsRegistrationFailure(t *testing.T) {
	registrationErr := errors.New("registration refused")
	repo := &fakeRuntimeNodeStore{registerReadyErr: registrationErr}
	health := &fakeDockerHealthChecker{}

	if err := startRuntimeHeartbeats(t.Context(), repo, health, time.Millisecond); !errors.Is(err, registrationErr) {
		t.Fatalf("error = %v, want registration failure", err)
	}
	if health.calls != 1 || repo.registerReadyCalls != 1 || repo.heartbeatCalls != 0 || repo.markUnhealthyCalls != 0 {
		t.Fatalf("calls = health:%d register:%d heartbeat:%d unhealthy:%d", health.calls, repo.registerReadyCalls, repo.heartbeatCalls, repo.markUnhealthyCalls)
	}
}

func TestRunHealthCheckedHeartbeatsCanceledCaller(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second, time.Hour} {
		t.Run(interval.String(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			repo := &fakeRuntimeNodeStore{}
			health := &fakeDockerHealthChecker{}

			if err := runHealthCheckedHeartbeats(ctx, repo, health, interval); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want caller cancellation", err)
			}
			if repo.registerReadyCalls != 1 || health.calls != 0 || repo.heartbeatCalls != 0 || repo.markUnhealthyCalls != 0 {
				t.Fatalf("calls = health:%d register:%d heartbeat:%d unhealthy:%d", health.calls, repo.registerReadyCalls, repo.heartbeatCalls, repo.markUnhealthyCalls)
			}
		})
	}
}

func TestRunHealthCheckedHeartbeatsTicksUntilRepositoryFailure(t *testing.T) {
	repositoryErr := errors.New("runtime write failed")
	dockerErr := errors.New("docker unavailable")
	for _, tc := range []struct {
		name                                     string
		healthErrors                             []error
		repo                                     fakeRuntimeNodeStore
		wantHealth, wantHeartbeat, wantUnhealthy int
	}{
		{"heartbeat failure", []error{nil}, fakeRuntimeNodeStore{heartbeatErr: repositoryErr}, 1, 1, 0},
		{"unhealthy write failure", []error{dockerErr}, fakeRuntimeNodeStore{markUnhealthyErr: repositoryErr}, 1, 0, 1},
		{"healthy tick continues", []error{nil, dockerErr}, fakeRuntimeNodeStore{markUnhealthyErr: repositoryErr}, 2, 1, 1},
		{"unhealthy tick recovers", []error{dockerErr, nil}, fakeRuntimeNodeStore{heartbeatErr: repositoryErr}, 2, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			health := &fakeDockerHealthChecker{errs: tc.healthErrors}
			if err := runHealthCheckedHeartbeats(ctx, &tc.repo, health, time.Millisecond); !errors.Is(err, repositoryErr) {
				t.Fatalf("error = %v, want repository failure", err)
			}
			if tc.repo.registerReadyCalls != 1 || health.calls != tc.wantHealth || tc.repo.heartbeatCalls != tc.wantHeartbeat || tc.repo.markUnhealthyCalls != tc.wantUnhealthy {
				t.Fatalf("calls = health:%d register:%d heartbeat:%d unhealthy:%d; want %d/1/%d/%d",
					health.calls, tc.repo.registerReadyCalls, tc.repo.heartbeatCalls, tc.repo.markUnhealthyCalls,
					tc.wantHealth, tc.wantHeartbeat, tc.wantUnhealthy)
			}
		})
	}
}

func TestHeartbeatOnceCanceledHealthCheckSkipsUnhealthyWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	repo := &fakeRuntimeNodeStore{}
	health := &fakeDockerHealthChecker{errs: []error{errors.New("docker unavailable")}}

	if err := heartbeatOnce(ctx, repo, health); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want caller cancellation", err)
	}
	if health.calls != 1 || repo.registerReadyCalls != 0 || repo.heartbeatCalls != 0 || repo.markUnhealthyCalls != 0 {
		t.Fatalf("calls = health:%d register:%d heartbeat:%d unhealthy:%d", health.calls, repo.registerReadyCalls, repo.heartbeatCalls, repo.markUnhealthyCalls)
	}
}

func TestCombinedHealthCheckerRequiresEveryCheck(t *testing.T) {
	if err := (combinedHealthChecker{}).Healthy(t.Context()); err != nil {
		t.Fatalf("empty health checks error = %v", err)
	}
	first := &fakeDockerHealthChecker{}
	second := &fakeDockerHealthChecker{}
	checker := combinedHealthChecker{checkers: []dockerHealthChecker{first, second}}
	if err := checker.Healthy(t.Context()); err != nil {
		t.Fatalf("healthy checks error = %v", err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("successful check calls = %d/%d, want 1/1", first.calls, second.calls)
	}

	failure := errors.New("firewall unavailable")
	second.errs = []error{nil, failure}
	next := &fakeDockerHealthChecker{}
	checker.checkers = append(checker.checkers, next)
	if err := checker.Healthy(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want failed health check", err)
	}
	if first.calls != 2 || second.calls != 2 || next.calls != 0 {
		t.Fatalf("failure check calls = %d/%d/%d, want 2/2/0", first.calls, second.calls, next.calls)
	}
}

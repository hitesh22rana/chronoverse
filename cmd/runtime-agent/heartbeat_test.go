package main

import (
	"context"
	"errors"
	"testing"
)

func TestStartRuntimeHeartbeatsStartupPingFailureExitsBeforeRegistration(t *testing.T) {
	repo := &fakeRuntimeNodeStore{}
	health := &fakeDockerHealthChecker{errs: []error{errors.New("docker down")}}

	err := startRuntimeHeartbeats(t.Context(), repo, health, 0)
	if err == nil {
		t.Fatal("startRuntimeHeartbeats() error = nil, want startup ping error")
	}
	if repo.registerReadyCalls != 0 || repo.heartbeatCalls != 0 || repo.markUnhealthyCalls != 0 {
		t.Fatalf("repo calls = register:%d heartbeat:%d unhealthy:%d, want none",
			repo.registerReadyCalls,
			repo.heartbeatCalls,
			repo.markUnhealthyCalls,
		)
	}
}

func TestHeartbeatOnceDockerFailureMarksUnhealthyAndContinues(t *testing.T) {
	repo := &fakeRuntimeNodeStore{}
	health := &fakeDockerHealthChecker{errs: []error{errors.New("docker down")}}

	if err := heartbeatOnce(t.Context(), repo, health); err != nil {
		t.Fatalf("heartbeatOnce() error = %v, want nil", err)
	}
	if repo.markUnhealthyCalls != 1 {
		t.Fatalf("markUnhealthy calls = %d, want 1", repo.markUnhealthyCalls)
	}
	if repo.heartbeatCalls != 0 {
		t.Fatalf("heartbeat calls = %d, want 0", repo.heartbeatCalls)
	}
}

func TestHeartbeatOnceDockerRecoveryMarksReadyAgain(t *testing.T) {
	repo := &fakeRuntimeNodeStore{}
	health := &fakeDockerHealthChecker{errs: []error{errors.New("docker down"), nil}}

	if err := heartbeatOnce(t.Context(), repo, health); err != nil {
		t.Fatalf("first heartbeatOnce() error = %v", err)
	}
	if err := heartbeatOnce(t.Context(), repo, health); err != nil {
		t.Fatalf("second heartbeatOnce() error = %v", err)
	}
	if repo.markUnhealthyCalls != 1 {
		t.Fatalf("markUnhealthy calls = %d, want 1", repo.markUnhealthyCalls)
	}
	if repo.heartbeatCalls != 1 {
		t.Fatalf("heartbeat calls = %d, want 1", repo.heartbeatCalls)
	}
}

func TestHeartbeatOnceRepositoryFailureIsFatal(t *testing.T) {
	repoErr := errors.New("postgres down")
	repo := &fakeRuntimeNodeStore{heartbeatErr: repoErr}
	health := &fakeDockerHealthChecker{errs: []error{nil}}

	err := heartbeatOnce(t.Context(), repo, health)
	if !errors.Is(err, repoErr) {
		t.Fatalf("heartbeatOnce() error = %v, want %v", err, repoErr)
	}
}

func TestHeartbeatOnceMarkUnhealthyFailureIsFatal(t *testing.T) {
	repoErr := errors.New("postgres down")
	repo := &fakeRuntimeNodeStore{markUnhealthyErr: repoErr}
	health := &fakeDockerHealthChecker{errs: []error{errors.New("docker down")}}

	err := heartbeatOnce(t.Context(), repo, health)
	if !errors.Is(err, repoErr) {
		t.Fatalf("heartbeatOnce() error = %v, want %v", err, repoErr)
	}
}

type fakeDockerHealthChecker struct {
	errs  []error
	calls int
}

func (f *fakeDockerHealthChecker) Healthy(context.Context) error {
	if f.calls >= len(f.errs) {
		f.calls++
		return nil
	}
	err := f.errs[f.calls]
	f.calls++
	return err
}

type fakeRuntimeNodeStore struct {
	registerReadyCalls int
	heartbeatCalls     int
	markUnhealthyCalls int
	registerReadyErr   error
	heartbeatErr       error
	markUnhealthyErr   error
}

func (f *fakeRuntimeNodeStore) RegisterReady(context.Context) error {
	f.registerReadyCalls++
	return f.registerReadyErr
}

func (f *fakeRuntimeNodeStore) Heartbeat(context.Context) error {
	f.heartbeatCalls++
	return f.heartbeatErr
}

func (f *fakeRuntimeNodeStore) MarkUnhealthy(context.Context) error {
	f.markUnhealthyCalls++
	return f.markUnhealthyErr
}

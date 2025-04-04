// Code generated by MockGen. DO NOT EDIT.
// Source: workflows.go
//
// Generated by this command:
//
//	mockgen -source=workflows.go -package=workflows -destination=./mock/workflows.go
//

// Package workflows is a generated GoMock package.
package workflows

import (
	context "context"
	reflect "reflect"

	workflows "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	gomock "go.uber.org/mock/gomock"
)

// MockRepository is a mock of Repository interface.
type MockRepository struct {
	ctrl     *gomock.Controller
	recorder *MockRepositoryMockRecorder
	isgomock struct{}
}

// MockRepositoryMockRecorder is the mock recorder for MockRepository.
type MockRepositoryMockRecorder struct {
	mock *MockRepository
}

// NewMockRepository creates a new mock instance.
func NewMockRepository(ctrl *gomock.Controller) *MockRepository {
	mock := &MockRepository{ctrl: ctrl}
	mock.recorder = &MockRepositoryMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockRepository) EXPECT() *MockRepositoryMockRecorder {
	return m.recorder
}

// CreateWorkflow mocks base method.
func (m *MockRepository) CreateWorkflow(ctx context.Context, userID, name, payload, kind string, interval, maxConsecutiveJobFailuresAllowed int32) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateWorkflow", ctx, userID, name, payload, kind, interval, maxConsecutiveJobFailuresAllowed)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateWorkflow indicates an expected call of CreateWorkflow.
func (mr *MockRepositoryMockRecorder) CreateWorkflow(ctx, userID, name, payload, kind, interval, maxConsecutiveJobFailuresAllowed any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateWorkflow", reflect.TypeOf((*MockRepository)(nil).CreateWorkflow), ctx, userID, name, payload, kind, interval, maxConsecutiveJobFailuresAllowed)
}

// GetWorkflow mocks base method.
func (m *MockRepository) GetWorkflow(ctx context.Context, jobID, userID string) (*workflows.GetWorkflowResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetWorkflow", ctx, jobID, userID)
	ret0, _ := ret[0].(*workflows.GetWorkflowResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetWorkflow indicates an expected call of GetWorkflow.
func (mr *MockRepositoryMockRecorder) GetWorkflow(ctx, jobID, userID any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetWorkflow", reflect.TypeOf((*MockRepository)(nil).GetWorkflow), ctx, jobID, userID)
}

// GetWorkflowByID mocks base method.
func (m *MockRepository) GetWorkflowByID(ctx context.Context, jobID string) (*workflows.GetWorkflowByIDResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetWorkflowByID", ctx, jobID)
	ret0, _ := ret[0].(*workflows.GetWorkflowByIDResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetWorkflowByID indicates an expected call of GetWorkflowByID.
func (mr *MockRepositoryMockRecorder) GetWorkflowByID(ctx, jobID any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetWorkflowByID", reflect.TypeOf((*MockRepository)(nil).GetWorkflowByID), ctx, jobID)
}

// IncrementWorkflowConsecutiveJobFailuresCount mocks base method.
func (m *MockRepository) IncrementWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID string) (bool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "IncrementWorkflowConsecutiveJobFailuresCount", ctx, workflowID)
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// IncrementWorkflowConsecutiveJobFailuresCount indicates an expected call of IncrementWorkflowConsecutiveJobFailuresCount.
func (mr *MockRepositoryMockRecorder) IncrementWorkflowConsecutiveJobFailuresCount(ctx, workflowID any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "IncrementWorkflowConsecutiveJobFailuresCount", reflect.TypeOf((*MockRepository)(nil).IncrementWorkflowConsecutiveJobFailuresCount), ctx, workflowID)
}

// ListWorkflows mocks base method.
func (m *MockRepository) ListWorkflows(ctx context.Context, userID, cursor string) (*workflows.ListWorkflowsResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListWorkflows", ctx, userID, cursor)
	ret0, _ := ret[0].(*workflows.ListWorkflowsResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListWorkflows indicates an expected call of ListWorkflows.
func (mr *MockRepositoryMockRecorder) ListWorkflows(ctx, userID, cursor any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListWorkflows", reflect.TypeOf((*MockRepository)(nil).ListWorkflows), ctx, userID, cursor)
}

// ResetWorkflowConsecutiveJobFailuresCount mocks base method.
func (m *MockRepository) ResetWorkflowConsecutiveJobFailuresCount(ctx context.Context, workflowID string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ResetWorkflowConsecutiveJobFailuresCount", ctx, workflowID)
	ret0, _ := ret[0].(error)
	return ret0
}

// ResetWorkflowConsecutiveJobFailuresCount indicates an expected call of ResetWorkflowConsecutiveJobFailuresCount.
func (mr *MockRepositoryMockRecorder) ResetWorkflowConsecutiveJobFailuresCount(ctx, workflowID any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ResetWorkflowConsecutiveJobFailuresCount", reflect.TypeOf((*MockRepository)(nil).ResetWorkflowConsecutiveJobFailuresCount), ctx, workflowID)
}

// TerminateWorkflow mocks base method.
func (m *MockRepository) TerminateWorkflow(ctx context.Context, jobID, userID string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "TerminateWorkflow", ctx, jobID, userID)
	ret0, _ := ret[0].(error)
	return ret0
}

// TerminateWorkflow indicates an expected call of TerminateWorkflow.
func (mr *MockRepositoryMockRecorder) TerminateWorkflow(ctx, jobID, userID any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "TerminateWorkflow", reflect.TypeOf((*MockRepository)(nil).TerminateWorkflow), ctx, jobID, userID)
}

// UpdateWorkflow mocks base method.
func (m *MockRepository) UpdateWorkflow(ctx context.Context, jobID, userID, name, payload string, interval, maxConsecutiveJobFailuresAllowed int32) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateWorkflow", ctx, jobID, userID, name, payload, interval, maxConsecutiveJobFailuresAllowed)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateWorkflow indicates an expected call of UpdateWorkflow.
func (mr *MockRepositoryMockRecorder) UpdateWorkflow(ctx, jobID, userID, name, payload, interval, maxConsecutiveJobFailuresAllowed any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateWorkflow", reflect.TypeOf((*MockRepository)(nil).UpdateWorkflow), ctx, jobID, userID, name, payload, interval, maxConsecutiveJobFailuresAllowed)
}

// UpdateWorkflowBuildStatus mocks base method.
func (m *MockRepository) UpdateWorkflowBuildStatus(ctx context.Context, jobID, buildStatus string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateWorkflowBuildStatus", ctx, jobID, buildStatus)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateWorkflowBuildStatus indicates an expected call of UpdateWorkflowBuildStatus.
func (mr *MockRepositoryMockRecorder) UpdateWorkflowBuildStatus(ctx, jobID, buildStatus any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateWorkflowBuildStatus", reflect.TypeOf((*MockRepository)(nil).UpdateWorkflowBuildStatus), ctx, jobID, buildStatus)
}

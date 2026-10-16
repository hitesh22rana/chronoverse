package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestListReadyRuntimeNodes(t *testing.T) {
	type contextKey struct{}
	for _, tc := range []struct {
		name  string
		nodes []*jobsmodel.RuntimeNode
		err   error
	}{
		{name: "empty"},
		{name: "ordered nodes and nil entries", nodes: []*jobsmodel.RuntimeNode{
			nil,
			{RuntimeNodeID: "second", RuntimeEndpoint: "tcp://second:2375"},
			nil,
			{RuntimeNodeID: "first", RuntimeEndpoint: "tcp://first:2375"},
		}},
		{name: "repository failure", err: status.Error(codes.Internal, "storage unavailable")},
		{name: "cancellation", err: status.Error(codes.Canceled, "caller canceled")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := jobsmock.NewMockRepository(ctrl)
			svc := jobs.New(validator.New(), repo, jobsmock.NewMockCache(ctrl))
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "runtime-selection"), time.Minute)
			defer cancel()
			if status.Code(tc.err) == codes.Canceled {
				cancel()
			}
			repo.EXPECT().ListReadyRuntimeNodes(gomock.Any()).DoAndReturn(func(got context.Context) ([]*jobsmodel.RuntimeNode, error) {
				require.Equal(t, ctx.Err(), got.Err())
				require.Equal(t, ctx.Done(), got.Done())
				require.Equal(t, "runtime-selection", got.Value(contextKey{}))
				wantDeadline, wantOK := ctx.Deadline()
				gotDeadline, gotOK := got.Deadline()
				require.Equal(t, wantOK, gotOK)
				require.Equal(t, wantDeadline, gotDeadline)
				return tc.nodes, tc.err
			})
			res, err := svc.ListReadyRuntimeNodes(ctx, &jobspb.ListReadyRuntimeNodesRequest{})
			if tc.err != nil {
				require.Same(t, tc.err, err)
				require.Nil(t, res)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, res)
			if len(tc.nodes) == 0 {
				require.Empty(t, res.Nodes)
				return
			}
			require.Len(t, res.Nodes, 2)
			require.Equal(t, "second", res.Nodes[0].GetRuntimeNodeId())
			require.Equal(t, "tcp://second:2375", res.Nodes[0].GetRuntimeEndpoint())
			require.Equal(t, "first", res.Nodes[1].GetRuntimeNodeId())
			require.Equal(t, "tcp://first:2375", res.Nodes[1].GetRuntimeEndpoint())
		})
	}
}

func TestGetReadyRuntimeNode(t *testing.T) {
	type contextKey struct{}
	for _, tc := range []struct {
		name string
		node *jobsmodel.RuntimeNode
		err  error
	}{
		{name: "selected endpoint", node: &jobsmodel.RuntimeNode{RuntimeNodeID: "winner", RuntimeEndpoint: "tcp://winner:2375"}},
		{name: "no healthy node", err: status.Error(codes.Unavailable, "no healthy runtime node is available")},
		{name: "storage failure", err: status.Error(codes.Internal, "storage unavailable")},
		{name: "cancellation", err: status.Error(codes.Canceled, "caller canceled")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := jobsmock.NewMockRepository(ctrl)
			svc := jobs.New(validator.New(), repo, jobsmock.NewMockCache(ctrl))
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "single-runtime-selection"), time.Minute)
			defer cancel()
			if status.Code(tc.err) == codes.Canceled {
				cancel()
			}
			repo.EXPECT().GetReadyRuntimeNode(gomock.Any()).DoAndReturn(func(got context.Context) (*jobsmodel.RuntimeNode, error) {
				require.Equal(t, ctx.Err(), got.Err())
				require.Equal(t, ctx.Done(), got.Done())
				require.Equal(t, "single-runtime-selection", got.Value(contextKey{}))
				wantDeadline, wantOK := ctx.Deadline()
				gotDeadline, gotOK := got.Deadline()
				require.Equal(t, wantOK, gotOK)
				require.Equal(t, wantDeadline, gotDeadline)
				return tc.node, tc.err
			})
			res, err := svc.GetReadyRuntimeNode(ctx, &jobspb.GetReadyRuntimeNodeRequest{})
			if tc.err != nil {
				require.Same(t, tc.err, err)
				require.Nil(t, res)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, res)
			require.Equal(t, "winner", res.GetRuntimeNodeId())
			require.Equal(t, "tcp://winner:2375", res.GetRuntimeEndpoint())
		})
	}
}

package heartbeat_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/repository/executor/workflows/heartbeat"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{
			name:    "success",
			data:    `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := heartbeat.New(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestHeartBeat_Execute(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr error
	}{
		{
			name:    "success",
			data:    `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
			wantErr: nil,
		},
		{
			name:    "success without headers",
			data:    `{"endpoint": "https://dummyjson.com/test"}`,
			wantErr: nil,
		},
		{
			name:    "missing endpoint",
			data:    `{"headers": {"Content-Type": "application/json"}}`,
			wantErr: status.Errorf(codes.InvalidArgument, "missing endpoint"),
		},
		{
			name:    "invalid endpoint",
			data:    `{"headers": {"Content-Type": "application/json"}, "endpoint": 123}`,
			wantErr: status.Errorf(codes.InvalidArgument, "invalid endpoint: 123"),
		},
		{
			name:    "invalid headers format",
			data:    `{"headers": "Content-Type: application/json", "endpoint": "https://dummyjson.com/test"}`,
			wantErr: status.Errorf(codes.InvalidArgument, "invalid headers format"),
		},
		{
			name:    "header value must be string",
			data:    `{"headers": {"Content-Type": ["application/json", 123]}, "endpoint": "https://dummyjson.com/test"}`,
			wantErr: status.Errorf(codes.InvalidArgument, "header value must be string"),
		},
		{
			name:    "invalid header value",
			data:    `{"headers": {"Content-Type": {"application/json": "application/json"}}, "endpoint": "https://dummyjson.com/test"}`,
			wantErr: status.Errorf(codes.InvalidArgument, "invalid header value for Content-Type"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := heartbeat.New(tt.data)
			if err != nil {
				t.Errorf("New() error = %v", err)
				return
			}

			gotErr := h.Execute(t.Context())
			assert.Equal(t, tt.wantErr, gotErr)
		})
	}
}

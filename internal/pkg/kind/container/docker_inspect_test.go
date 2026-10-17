package container_test

import (
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

type inspectTransport func(*http.Request) (*http.Response, error)

func (f inspectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDockerInspectStateAndFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, message         string
		httpStatus                  int
		pingFailure, inspectFailure bool
		code                        codes.Code
		state                       *container.State
	}{
		{name: "running", body: `{"State":{"Running":true,"ExitCode":0,"Status":"running"}}`, state: &container.State{Running: true, Status: "running"}},
		{name: "exited", body: `{"State":{"Running":false,"ExitCode":137,"Status":"exited"}}`, state: &container.State{ExitCode: 137, Status: "exited"}},
		{name: "missing base", body: `{}`, code: codes.Aborted, message: "has no state"},
		{name: "missing state", body: `{"Id":"container-42","State":null}`, code: codes.Aborted, message: "has no state"},
		{name: "not found", httpStatus: http.StatusNotFound, body: `{"message":"No such container"}`, code: codes.NotFound, message: "container not found"},
		{name: "daemon rejection", httpStatus: http.StatusInternalServerError, body: `{"message":"storage failure"}`, code: codes.Aborted, message: "storage failure"},
		{name: "invalid response", body: `{"State":`, code: codes.Aborted, message: "failed to inspect container container-42"},
		{name: "connection lost after ping", inspectFailure: true, code: codes.Unavailable, message: "docker daemon unavailable"},
		{name: "ping failure", pingFailure: true, code: codes.Internal, message: "failed to ping docker client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pings, inspections := 0, 0
			transport := inspectTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/_ping" {
					pings++
					require.Contains(t, []string{http.MethodHead, http.MethodGet}, r.Method)
					if tc.pingFailure {
						return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Api-Version": []string{"1.51"}}, Body: io.NopCloser(strings.NewReader("OK")), Request: r}, nil
				}
				inspections++
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/v1.51/containers/container-42/json", r.URL.Path)
				if tc.inspectFailure {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
				}
				responseStatus := tc.httpStatus
				if responseStatus == 0 {
					responseStatus = http.StatusOK
				}
				return &http.Response{StatusCode: responseStatus, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			cli, err := client.NewClientWithOpts(client.WithHost("http://docker.test"), client.WithVersion("1.51"), client.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cli.Close()) })
			workflow := &container.DockerWorkflow{Client: cli}
			state, err := workflow.Inspect(t.Context(), "container-42")
			require.Equal(t, tc.code, status.Code(err))
			require.Equal(t, tc.state, state)
			require.Positive(t, pings)
			if tc.code == codes.OK {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.message)
			}
			if tc.pingFailure {
				require.Zero(t, inspections)
			} else {
				require.Equal(t, 1, inspections)
			}
		})
	}
}

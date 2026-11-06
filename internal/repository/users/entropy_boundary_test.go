//nolint:testpackage // Isolates the actual bcrypt entropy-provider failure before database access.
package users

import (
	"context"
	"crypto/rand"
	"flag"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type unavailableEntropy struct{}

func (unavailableEntropy) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRegistrationReportsEntropyFailureBeforeDatabaseAccess(t *testing.T) {
	const child = "CHRONOVERSE_TEST_REGISTRATION_ENTROPY_FAILURE"
	if os.Getenv(child) == "1" {
		repo := New(nil, nil)
		repo.tp = noop.NewTracerProvider().Tracer("entropy-boundary")
		previous := rand.Reader
		rand.Reader = unavailableEntropy{}
		result, token, err := repo.RegisterUser(t.Context(), "person@example.com", "9gR!bQ7@zL2#vN5$", "entropy-before-database")
		rand.Reader = previous
		require.Nil(t, result)
		require.Empty(t, token)
		require.Equal(t, codes.Internal, status.Code(err))
		require.Contains(t, err.Error(), "failed to hash password")
		require.Contains(t, err.Error(), io.ErrUnexpectedEOF.Error())
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestRegistrationReportsEntropyFailureBeforeDatabaseAccess$")
	command.Env = append(os.Environ(), child+"=1")
	if directory := flag.Lookup("test.gocoverdir").Value.String(); directory != "" {
		command.Env = append(command.Env, "GOCOVERDIR="+directory)
	}
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

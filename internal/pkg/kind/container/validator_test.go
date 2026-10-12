package container_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

func TestExtractAndValidateContainerDetailsInvalidPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		code    codes.Code
		message string
	}{
		{"malformed JSON", `{`, codes.InvalidArgument, "invalid payload format"},
		{"array payload", `[]`, codes.InvalidArgument, "invalid payload format"},
		{"null payload", `null`, codes.InvalidArgument, "image is missing or invalid"},
		{"missing image", `{}`, codes.InvalidArgument, "image is missing or invalid"},
		{"empty image", `{"image":""}`, codes.InvalidArgument, "image is missing or invalid"},
		{"numeric image", `{"image":7}`, codes.InvalidArgument, "image is missing or invalid"},
		{"malformed image", `{"image":"not a reference %%%"}`, codes.InvalidArgument, "image reference is invalid:"},
		{"disallowed registry", `{"image":"evil.example/image:latest"}`, codes.InvalidArgument, "is not allowed"},
		{"invalid duration", `{"image":"alpine","timeout":"tomorrow"}`, codes.InvalidArgument, "timeout is invalid"},
		{"zero duration", `{"image":"alpine","timeout":"0s"}`, codes.InvalidArgument, "timeout is invalid"},
		{"negative duration", `{"image":"alpine","timeout":"-1s"}`, codes.InvalidArgument, "timeout is invalid"},
		{"timeout over maximum", `{"image":"alpine","timeout":"1h1ns"}`, codes.FailedPrecondition, "timeout exceeds maximum limit of 60 minutes"},
		{"mixed command elements", `{"image":"alpine","cmd":["echo",7]}`, codes.InvalidArgument, "cmd contains non-string elements"},
		{"numeric environment value", `{"image":"alpine","env":{"PORT":8080}}`, codes.InvalidArgument, "env contains non-string values"},
		{"null environment value", `{"image":"alpine","env":{"PORT":null}}`, codes.InvalidArgument, "env contains non-string values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := container.ExtractAndValidateContainerDetails(tt.payload)
			if details == nil {
				t.Fatal("expected details alongside validation error")
			}
			if status.Code(err) != tt.code || !strings.Contains(status.Convert(err).Message(), tt.message) {
				t.Fatalf("error = %v, want %s containing %q", err, tt.code, tt.message)
			}
		})
	}
}

func TestExtractAndValidateContainerDetailsValidPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		timeout time.Duration
		cmd     []string
		env     []string
	}{
		{"defaults", `{"image":"alpine"}`, 30 * time.Second, []string{}, []string{}},
		{"minimum positive timeout", `{"image":"alpine","timeout":"1ns"}`, time.Nanosecond, []string{}, []string{}},
		{"maximum timeout", `{"image":"alpine","timeout":"1h"}`, time.Hour, []string{}, []string{}},
		{"numeric timeout uses default", `{"image":"alpine","timeout":15}`, 30 * time.Second, []string{}, []string{}},
		{"null timeout uses default", `{"image":"alpine","timeout":null}`, 30 * time.Second, []string{}, []string{}},
		{"unsupported command and environment ignored", `{"image":"alpine","cmd":"echo hello","env":["A=B"]}`, 30 * time.Second, []string{}, []string{}},
		{"empty command and environment", `{"image":"alpine","cmd":[],"env":{}}`, 30 * time.Second, []string{}, []string{}},
		{
			"command and environment preserved",
			`{"image":"alpine","timeout":"45s","cmd":["sh","-c","echo hello"],"env":{"MESSAGE":"hello world"}}`,
			45 * time.Second,
			[]string{"sh", "-c", "echo hello"},
			[]string{"MESSAGE=hello world"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := container.ExtractAndValidateContainerDetails(tt.payload)
			if err != nil {
				t.Fatalf("validate payload: %v", err)
			}
			want := &container.Details{TimeOut: tt.timeout, Image: "alpine", Cmd: tt.cmd, Env: tt.env}
			if !reflect.DeepEqual(details, want) {
				t.Fatalf("details = %+v, want %+v", details, want)
			}
		})
	}
}

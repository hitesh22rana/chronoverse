package container

import (
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	containerWorkflowDefaultExecutionTimeout = 30 * time.Second
	containerWorkflowMaxExecutionTimeout     = 1 * time.Hour
)

// Details defines the configuration for executing a container-based workflow step.
// It specifies the maximum execution timeout, the container image to use, the command to run
// inside the container, and any environment variables to set for the container process.
type Details struct {
	TimeOut time.Duration // Maximum duration to allow the container to run
	Image   string        // Name and tag of the container image to execute
	Cmd     []string      // Command and arguments to run inside the container
	Env     []string      // Environment variables to set in the container (in "KEY=VALUE" format)
}

// ExtractAndValidateContainerDetails extracts the container details from the workflow payload.
func ExtractAndValidateContainerDetails(payload string) (*Details, error) {
	var (
		details = &Details{
			TimeOut: containerWorkflowDefaultExecutionTimeout,
			Image:   "",
			Cmd:     []string{},
			Env:     []string{},
		}
		err  error
		data map[string]any
	)

	if err = json.Unmarshal([]byte(payload), &data); err != nil {
		return details, status.Error(codes.InvalidArgument, "invalid payload format")
	}

	timeout, ok := data["timeout"].(string)
	if ok {
		details.TimeOut, err = time.ParseDuration(timeout)
		if err != nil {
			return details, status.Error(codes.InvalidArgument, "timeout is invalid")
		}
	}

	if details.TimeOut <= 0 {
		return details, status.Error(codes.InvalidArgument, "timeout is invalid")
	}

	if details.TimeOut > containerWorkflowMaxExecutionTimeout {
		return details, status.Errorf(codes.FailedPrecondition, "timeout exceeds maximum limit of %.0f minutes", containerWorkflowMaxExecutionTimeout.Minutes())
	}

	image, ok := data["image"].(string)
	if !ok || image == "" {
		return details, status.Error(codes.InvalidArgument, "image is missing or invalid")
	}
	details.Image = image

	// Command is an optional field
	cmd, ok := data["cmd"].([]any)
	if ok {
		// If cmd is provided, convert all elements to strings
		for _, c := range cmd {
			cStr, _ok := c.(string)
			if !_ok {
				return details, status.Error(codes.InvalidArgument, "cmd contains non-string elements")
			}
			details.Cmd = append(details.Cmd, cStr)
		}
	}

	// Environment variables are optional
	env, ok := data["env"].(map[string]any)
	if ok {
		// Convert the map to a slice of strings
		for key, value := range env {
			valueStr, _ok := value.(string)
			if !_ok {
				return details, status.Error(codes.InvalidArgument, "env contains non-string values")
			}
			details.Env = append(details.Env, fmt.Sprintf("%s=%s", key, valueStr))
		}
	}

	return details, nil
}

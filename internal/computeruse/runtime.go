package computeruse

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	CodeRuntimeUnavailable = "COMPUTER_RUNTIME_UNAVAILABLE"
	CodeStartFailed        = "COMPUTER_START_FAILED"
	CodeCallFailed         = "COMPUTER_CALL_FAILED"
	CodeTimeout            = "COMPUTER_TIMEOUT"
)

// RuntimeError carries the stable tool-facing error code for Computer Use
// failures. The server maps Code directly into its public error envelope.
type RuntimeError struct {
	Code    string
	Message string
	Err     error
}

func (e *RuntimeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *RuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Runtime contains the executable paths required to start the MCPX Computer
// Use adapter. Node runs the adapter and BinaryPath points at the independent
// open-computer-use native MCP server.
type Runtime struct {
	NodePath   string
	BinaryPath string
}

// ResolveRuntime resolves the Node and open-computer-use executables. Explicit
// environment overrides take precedence over PATH discovery.
func ResolveRuntime() (Runtime, error) {
	node, err := resolveExecutable(
		"MCPX_COMPUTER_USE_NODE",
		[]string{"node"},
	)
	if err != nil {
		return Runtime{}, err
	}
	binary, err := resolveExecutable(
		"MCPX_COMPUTER_USE_BINARY",
		[]string{"open-computer-use", "ocu"},
	)
	if err != nil {
		return Runtime{}, err
	}
	return Runtime{NodePath: node, BinaryPath: binary}, nil
}

func resolveExecutable(envName string, candidates []string) (string, error) {
	if explicit := strings.TrimSpace(os.Getenv(envName)); explicit != "" {
		if err := validateExecutable(explicit); err != nil {
			return "", runtimeUnavailable(fmt.Errorf("%s does not point to a runnable file: %w", envName, err))
		}
		return explicit, nil
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err == nil {
			if validateErr := validateExecutable(path); validateErr == nil {
				return path, nil
			}
		}
	}
	return "", runtimeUnavailable(errors.New(
		"Computer Use requires Node.js 18+ and open-computer-use 0.3.5+; install with: npm i -g open-computer-use",
	))
}

func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("path is a directory")
	}
	return nil
}

func runtimeUnavailable(err error) error {
	return &RuntimeError{Code: CodeRuntimeUnavailable, Message: err.Error(), Err: err}
}

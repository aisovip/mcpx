package computeruse

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveRuntimeUsesExplicitOverrides(t *testing.T) {
	dir := t.TempDir()
	node := writeTestExecutable(t, dir, "custom-node")
	binary := writeTestExecutable(t, dir, "custom-open-computer-use")
	t.Setenv("MCPX_COMPUTER_USE_NODE", node)
	t.Setenv("MCPX_COMPUTER_USE_BINARY", binary)

	got, err := ResolveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if got.NodePath != node || got.BinaryPath != binary {
		t.Fatalf("runtime = %+v, want node=%s binary=%s", got, node, binary)
	}
}

func TestResolveRuntimeFindsNodeAndBinaryOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH executable fixtures are Unix-specific")
	}
	dir := t.TempDir()
	node := writeTestExecutable(t, dir, "node")
	binary := writeTestExecutable(t, dir, "open-computer-use")
	t.Setenv("MCPX_COMPUTER_USE_NODE", "")
	t.Setenv("MCPX_COMPUTER_USE_BINARY", "")
	t.Setenv("PATH", dir)

	got, err := ResolveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if got.NodePath != node || got.BinaryPath != binary {
		t.Fatalf("runtime = %+v, want node=%s binary=%s", got, node, binary)
	}
}

func TestResolveRuntimeReportsUnavailableRuntime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPX_COMPUTER_USE_NODE", "")
	t.Setenv("MCPX_COMPUTER_USE_BINARY", "")
	t.Setenv("PATH", dir)

	_, err := ResolveRuntime()
	if err == nil {
		t.Fatal("expected runtime error")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error = %T %v, want *RuntimeError", err, err)
	}
	if runtimeErr.Code != CodeRuntimeUnavailable {
		t.Fatalf("code = %q, want %q", runtimeErr.Code, CodeRuntimeUnavailable)
	}
}

func writeTestExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

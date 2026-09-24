//go:build darwin

package browseruse

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestBrowserPipeCandidatesOnDarwinFindsUnixSockets(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mcpx-pipe-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "backend.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.WriteFile(filepath.Join(dir, "not-a-socket"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := browserPipeCandidatesFromRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{socketPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pipe candidates = %q, want %q", got, want)
	}
}

func TestBrowserPipeCandidatesOnDarwinMissingRootIsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	got, err := browserPipeCandidatesFromRoot(missing)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("pipe candidates = %q, want none", got)
	}
}

func TestOpenBrowserPipeOnDarwinUsesFramedRPC(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mcpx-pipe-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "backend.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		payload, err := readFrame(conn)
		if err != nil {
			serverDone <- err
			return
		}
		if got := string(payload); got != `{"id":1,"jsonrpc":"2.0","method":"getInfo","params":{}}` {
			serverDone <- fmt.Errorf("unexpected request: %s", got)
			return
		}
		response := []byte(`{"jsonrpc":"2.0","id":1,"result":{"type":"extension","family":"chrome","metadata":{"extensionId":"hehggadaopoacecdllhhajmbjkdcmajg","extensionInstanceId":"instance-1"}}}`)
		serverDone <- writeFrame(conn, response)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := (Client{Pipe: socketPath}).GetInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if info.Type != "extension" || info.Family != "chrome" || info.Metadata.ExtensionInstanceID != "instance-1" {
		t.Fatalf("info = %+v", info)
	}
}

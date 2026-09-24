//go:build darwin

package browseruse

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
)

const darwinBrowserPipeRoot = "/tmp/codex-browser-use"

func browserPipeCandidates() ([]string, error) {
	return browserPipeCandidatesFromRoot(darwinBrowserPipeRoot)
}

func browserPipeCandidatesFromRoot(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSocket != 0 {
			result = append(result, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(result)
	return result, nil
}

func openBrowserPipe(ctx context.Context, path string) (io.ReadWriteCloser, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", path)
}

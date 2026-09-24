//go:build darwin

package browseruse

import (
	"os"
	"path/filepath"
	"strings"
)

func DiscoverInstalledOfficial() ([]Installation, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, nil
	}
	appSupport := filepath.Join(home, "Library", "Application Support")
	roots := []browserInstallRoot{
		{Family: "chrome", Path: filepath.Join(appSupport, "Google", "Chrome")},
		{Family: "chromium", Path: filepath.Join(appSupport, "Chromium")},
		{Family: "brave", Path: filepath.Join(appSupport, "BraveSoftware", "Brave-Browser")},
		{Family: "edge", Path: filepath.Join(appSupport, "Microsoft Edge")},
	}
	return discoverInstalledOfficialFromRoots(roots), nil
}

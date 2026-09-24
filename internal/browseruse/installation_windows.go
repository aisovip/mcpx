//go:build windows

package browseruse

import (
	"os"
	"path/filepath"
	"strings"
)

func DiscoverInstalledOfficial() ([]Installation, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		if home, err := os.UserHomeDir(); err == nil {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
	}
	if localAppData == "" {
		return nil, nil
	}
	roots := []browserInstallRoot{
		{Family: "edge", Path: filepath.Join(localAppData, "Microsoft", "Edge", "User Data")},
		{Family: "chrome", Path: filepath.Join(localAppData, "Google", "Chrome", "User Data")},
		{Family: "brave", Path: filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "User Data")},
		{Family: "chromium", Path: filepath.Join(localAppData, "Chromium", "User Data")},
	}
	return discoverInstalledOfficialFromRoots(roots), nil
}

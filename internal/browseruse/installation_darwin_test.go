//go:build darwin

package browseruse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverInstalledOfficialOnDarwinFindsChromeManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const extensionID = "hehggadaopoacecdllhhajmbjkdcmajg"
	manifestPath := filepath.Join(
		home,
		"Library", "Application Support", "Google", "Chrome",
		"Default", "Extensions", extensionID, "1.2.3_0", "manifest.json",
	)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}

	manifest := extensionManifest{
		ManifestVersion: 3,
		Name:            "ChatGPT",
		Version:         "1.2.3",
		UpdateURL:       "https://clients2.google.com/service/update2/crx",
		Action: struct {
			DefaultTitle string `json:"default_title"`
		}{DefaultTitle: "ChatGPT"},
		Background: struct {
			ServiceWorker string `json:"service_worker"`
		}{ServiceWorker: "background.js"},
		Commands: map[string]any{"open-codex-side-panel": map[string]any{}},
		ContentScripts: []struct {
			Matches []string `json:"matches"`
			JS      []string `json:"js"`
		}{{Matches: []string{"https://chatgpt.com/*"}}},
		Permissions: []string{"debugger", "nativeMessaging", "tabs", "scripting"},
		SidePanel: struct {
			DefaultPath string `json:"default_path"`
		}{DefaultPath: "codex-sidepanel/index.html"},
	}
	manifest.ContentSecurityPolicy.ExtensionPages = "connect-src 'self' https://chatgpt.com https://api.openai.com;"
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	installations, err := DiscoverInstalledOfficial()
	if err != nil {
		t.Fatal(err)
	}
	if len(installations) != 1 {
		t.Fatalf("installations = %+v, want one Chrome installation", installations)
	}
	got := installations[0]
	if got.Family != "chrome" || got.Profile != "Default" || got.ExtensionID != extensionID || got.Version != "1.2.3" {
		t.Fatalf("installation = %+v", got)
	}
}

package browseruse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const OfficialBrowserHelpURL = "https://learn.chatgpt.com/docs/chrome-extension"

type Installation struct {
	Family       string `json:"family"`
	Profile      string `json:"profile"`
	ExtensionID  string `json:"extension_id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	ManifestPath string `json:"manifest_path,omitempty"`
}

type Discovery struct {
	Installations []Installation
	Backends      []Backend
	PipeCount     int
}

type browserInstallRoot struct {
	Family string
	Path   string
}

func discoverInstalledOfficialFromRoots(roots []browserInstallRoot) []Installation {
	found := map[string]Installation{}
	for _, root := range roots {
		profiles, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		for _, profile := range profiles {
			if !profile.IsDir() {
				continue
			}
			extensionsDir := filepath.Join(root.Path, profile.Name(), "Extensions")
			extensions, err := os.ReadDir(extensionsDir)
			if err != nil {
				continue
			}
			for _, extension := range extensions {
				if !extension.IsDir() {
					continue
				}
				versionsDir := filepath.Join(extensionsDir, extension.Name())
				versions, err := os.ReadDir(versionsDir)
				if err != nil {
					continue
				}
				for _, version := range versions {
					if !version.IsDir() {
						continue
					}
					manifestPath := filepath.Join(versionsDir, version.Name(), "manifest.json")
					payload, err := os.ReadFile(manifestPath)
					if err != nil {
						continue
					}
					var manifest extensionManifest
					if json.Unmarshal(payload, &manifest) != nil || !isOfficialBrowserManifest(manifest) {
						continue
					}
					key := strings.ToLower(root.Family) + "\x00" + strings.ToLower(profile.Name()) + "\x00" + strings.ToLower(extension.Name())
					candidate := Installation{
						Family:       root.Family,
						Profile:      profile.Name(),
						ExtensionID:  extension.Name(),
						Name:         manifest.Name,
						Version:      manifest.Version,
						ManifestPath: manifestPath,
					}
					if current, ok := found[key]; !ok || candidate.Version > current.Version {
						found[key] = candidate
					}
				}
			}
		}
	}
	result := make([]Installation, 0, len(found))
	for _, installation := range found {
		result = append(result, installation)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Family != result[j].Family {
			return result[i].Family < result[j].Family
		}
		if result[i].Profile != result[j].Profile {
			return result[i].Profile < result[j].Profile
		}
		return result[i].ExtensionID < result[j].ExtensionID
	})
	return result
}

func (d Discovery) State() string {
	if len(d.Backends) > 0 {
		return "connected"
	}
	if len(d.Installations) > 0 {
		return "installed_disconnected"
	}
	return "not_installed"
}

type extensionManifest struct {
	ManifestVersion int    `json:"manifest_version"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Version         string `json:"version"`
	UpdateURL       string `json:"update_url"`
	Action          struct {
		DefaultTitle string `json:"default_title"`
	} `json:"action"`
	Background struct {
		ServiceWorker string `json:"service_worker"`
	} `json:"background"`
	Commands       map[string]any `json:"commands"`
	ContentScripts []struct {
		Matches []string `json:"matches"`
		JS      []string `json:"js"`
	} `json:"content_scripts"`
	ContentSecurityPolicy struct {
		ExtensionPages string `json:"extension_pages"`
	} `json:"content_security_policy"`
	Permissions []string `json:"permissions"`
	SidePanel   struct {
		DefaultPath string `json:"default_path"`
	} `json:"side_panel"`
}

func isOfficialBrowserManifest(manifest extensionManifest) bool {
	if manifest.ManifestVersion != 3 || strings.TrimSpace(manifest.Background.ServiceWorker) != "background.js" {
		return false
	}
	updateURL := strings.ToLower(strings.TrimSpace(manifest.UpdateURL))
	if !strings.HasPrefix(updateURL, "https://edge.microsoft.com/extensionwebstorebase/") && !strings.HasPrefix(updateURL, "https://clients2.google.com/service/update2/crx") {
		return false
	}
	if strings.TrimSpace(manifest.SidePanel.DefaultPath) != "codex-sidepanel/index.html" {
		return false
	}
	if _, ok := manifest.Commands["open-codex-side-panel"]; !ok {
		return false
	}
	for _, required := range []string{"debugger", "nativeMessaging", "tabs", "scripting"} {
		if !containsString(manifest.Permissions, required) {
			return false
		}
	}
	hasChatGPTScript := false
	for _, script := range manifest.ContentScripts {
		if containsString(script.Matches, "https://chatgpt.com/*") {
			hasChatGPTScript = true
			break
		}
	}
	if !hasChatGPTScript {
		return false
	}
	csp := manifest.ContentSecurityPolicy.ExtensionPages
	if !strings.Contains(csp, "https://chatgpt.com") || !strings.Contains(csp, "https://api.openai.com") {
		return false
	}
	name := strings.TrimSpace(manifest.Name)
	title := strings.TrimSpace(manifest.Action.DefaultTitle)
	return strings.EqualFold(name, "ChatGPT") || strings.EqualFold(title, "ChatGPT")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func matchesOfficialInstallation(info Info, installations []Installation) bool {
	extensionID := strings.TrimSpace(info.Metadata.ExtensionID)
	if extensionID == "" {
		return false
	}
	family := strings.ToLower(strings.TrimSpace(info.Family))
	for _, installation := range installations {
		if !strings.EqualFold(strings.TrimSpace(installation.ExtensionID), extensionID) {
			continue
		}
		installedFamily := strings.ToLower(strings.TrimSpace(installation.Family))
		if family == "" || installedFamily == "" || family == installedFamily {
			return true
		}
	}
	return false
}

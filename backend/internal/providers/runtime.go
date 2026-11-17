package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func managedNode() string {
	root := os.Getenv("FREEWAY_PROVIDER_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".freeway", "providers")
	}
	data, err := os.ReadFile(filepath.Join(root, "install.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version int `json:"schemaVersion"`
		Node    struct {
			Path string `json:"path"`
		} `json:"node"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 || !filepath.IsAbs(manifest.Node.Path) {
		return ""
	}
	if info, err := os.Stat(manifest.Node.Path); err != nil || info.IsDir() {
		return ""
	}
	return manifest.Node.Path
}

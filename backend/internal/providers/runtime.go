package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func ProviderHome() string {
	if root := os.Getenv("FREEWAY_PROVIDER_HOME"); root != "" {
		return root
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".freeway", "providers")
}

// CLIExecutable resolves explicit overrides, managed native installations, then PATH.
// npm command shims are never executed through a shell.
func CLIExecutable(id string) (string, error) {
	if id != "opencode" && id != "cline" {
		return "", errors.New("unknown_cli")
	}

	if override := os.Getenv(strings.ToUpper(id) + "_BIN"); override != "" {
		path, err := filepath.Abs(override)
		if err == nil && usableCLI(path) {
			return path, nil
		}
		return "", failure("cli_not_installed", 409)
	}
	var manifest struct {
		Version int `json:"schemaVersion"`
		Tools   map[string]struct {
			Path string `json:"path"`
		} `json:"tools"`
	}
	if data, err := os.ReadFile(filepath.Join(ProviderHome(), "install.json")); err == nil && json.Unmarshal(data, &manifest) == nil && manifest.Version == 1 {
		if path := manifest.Tools[id].Path; filepath.IsAbs(path) && usableCLI(path) {
			return path, nil
		}
	}
	if path, err := exec.LookPath(id); err == nil {
		if usableCLI(path) {
			return path, nil
		}
		for _, candidate := range npmNativeCandidates(path, id) {
			if usableCLI(candidate) {
				return candidate, nil
			}
		}
	}
	if runtime.GOOS == "windows" {
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			for _, path := range windowsNativeCandidates(dir, id, runtime.GOARCH) {
				if usableCLI(path) {
					return path, nil
				}
			}
		}
	}
	if id == "opencode" {
		home, _ := os.UserHomeDir()
		for _, path := range []string{filepath.Join(home, ".opencode", "bin", "opencode"), "/opt/homebrew/bin/opencode", "/usr/local/bin/opencode"} {
			if usableCLI(path) {
				return path, nil
			}
		}
	}
	return "", failure("cli_not_installed", 409)
}

func npmNativeCandidates(shim, id string) []string {
	actual, err := filepath.EvalSymlinks(shim)
	if err != nil {
		actual = shim
	}
	packages := []string{filepath.Join(filepath.Dir(shim), "node_modules"), filepath.Dir(filepath.Dir(filepath.Dir(actual)))}
	name, _, err := installPackage(id, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil
	}
	executable := id
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	var candidates []string
	for _, root := range packages {
		if id == "cline" {
			candidates = append(candidates, filepath.Join(root, "cline", "bin", ".cline"))
		}
		packageName := id
		if id == "opencode" {
			packageName = "opencode-ai"
		}
		for _, base := range []string{root, filepath.Join(root, packageName, "node_modules")} {
			candidates = append(candidates, filepath.Join(base, filepath.FromSlash(name), "bin", executable))
			if strings.HasSuffix(name, "-baseline") {
				candidates = append(candidates, filepath.Join(base, filepath.FromSlash(strings.TrimSuffix(name, "-baseline")), "bin", executable))
			}
		}
	}
	return candidates
}

func windowsNativeCandidates(dir, id, arch string) []string {
	if arch == "amd64" {
		arch = "x64"
	}
	packages := filepath.Join(dir, "node_modules")
	if id == "opencode" {
		return []string{filepath.Join(packages, "opencode-ai", "bin", "opencode.exe"), filepath.Join(packages, "opencode-windows-"+arch+"-baseline", "bin", "opencode.exe"), filepath.Join(packages, "opencode-windows-"+arch, "bin", "opencode.exe")}
	}
	return []string{filepath.Join(packages, "cline", "bin", ".cline"), filepath.Join(packages, "@cline", "cli-windows-"+arch, "bin", "cline.exe"), filepath.Join(packages, "cline", "node_modules", "@cline", "cli-windows-"+arch, "bin", "cline.exe")}
}

func usableCLI(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "node", "node.exe", "nodejs", "bun", "bun.exe", "deno", "deno.exe", "npm", "npm.exe", "npx", "npx.exe":
		return false
	}
	if path == "" || strings.HasSuffix(strings.ToLower(path), ".cmd") || strings.HasSuffix(strings.ToLower(path), ".bat") {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var header [256]byte
	n, _ := file.Read(header[:])
	first := bytes.SplitN(header[:n], []byte("\n"), 2)[0]
	// Resolve the official native package instead of launching an interpreter or shell wrapper.
	return !bytes.HasPrefix(first, []byte("#!"))
}

// explicitCLIPath rejects JavaScript and shell shims before starting an explicitly configured CLI.
func explicitCLIPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		var err error
		if filepath.Base(path) == path {
			path, err = exec.LookPath(path)
		} else {
			path, err = filepath.Abs(path)
		}
		if err != nil {
			return "", failure("cli_not_installed", 409)
		}
	}
	if !usableCLI(path) {
		return "", failure("cli_not_installed", 409)
	}
	return path, nil
}

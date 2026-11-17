package providers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed versions.json
var installPinsJSON []byte

// InstallOptions selects verified native CLI releases. Empty Tools means HTTP API only.
// Nil Tools uses both CLIs for install and the saved profile for update/check.
type InstallOptions struct {
	Action string
	Root   string
	Tools  []string
}

type InstallComponent struct {
	ID                 string `json:"id"`
	Path               string `json:"path"`
	InstalledVersion   string `json:"installedVersion"`
	RecommendedVersion string `json:"recommendedVersion"`
	UpdateAvailable    bool   `json:"updateAvailable"`
	Status             string `json:"status"`
}

type Installation struct {
	OK              bool               `json:"ok"`
	Command         string             `json:"command"`
	Root            string             `json:"root"`
	Components      []InstallComponent `json:"components"`
	RestartRequired bool               `json:"restartRequired"`
}

type installedTool struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type installManifest struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Tools         map[string]installedTool `json:"tools"`
	SelectedTools []string                 `json:"selectedTools"`
	UpdatedAt     time.Time                `json:"updatedAt"`
}

type nativeInstaller struct {
	client  *http.Client
	version func(context.Context, string) (string, error)
	pins    map[string]string
	goos    string
	goarch  string
}

// Install downloads official native packages directly, without npm, Node, or JavaScript.
// It preserves older binaries and publishes install.json only after final verification.
func Install(ctx context.Context, options InstallOptions) (Installation, error) {
	client := &http.Client{CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !installRegistryURL(request.URL) {
			return failure("unsafe_package_url", 502)
		}
		return nil
	}}
	return (nativeInstaller{client: client, version: installNativeVersion, goos: runtime.GOOS, goarch: runtime.GOARCH}).run(ctx, options)
}

func (installer nativeInstaller) run(ctx context.Context, options InstallOptions) (result Installation, err error) {
	defer func() {
		if ctx.Err() != nil {
			result.OK = false
			err = ctx.Err()
		}
	}()
	if options.Action == "" {
		options.Action = "install"
	}
	if options.Action != "install" && options.Action != "update" && options.Action != "check" {
		return result, failure("invalid_install_action", 400)
	}
	root := options.Root
	if root == "" {
		root = ProviderHome()
	}
	root, err = installRoot(root)
	if err != nil {
		return result, err
	}
	result = Installation{Command: options.Action, Root: root, Components: []InstallComponent{}}
	if installer.pins == nil {
		if json.Unmarshal(installPinsJSON, &installer.pins) != nil {
			return result, failure("invalid_version_pins", 500)
		}
	}
	for _, id := range []string{"opencode", "cline"} {
		if !installVersionPattern.MatchString(installer.pins[id]) {
			return result, failure("invalid_version_pins", 500)
		}
	}
	previous, err := readInstallManifest(root)
	if err != nil {
		return result, err
	}
	tools := options.Tools
	if tools == nil {
		tools = []string{"opencode", "cline"}
		if options.Action != "install" && previous.SelectedTools != nil {
			tools = previous.SelectedTools
		}
	}
	seen := map[string]bool{}
	for _, id := range tools {
		if (id != "opencode" && id != "cline") || seen[id] {
			return result, failure("invalid_install_tools", 400)
		}
		seen[id] = true
		if _, _, err := installPackage(id, installer.goos, installer.goarch); err != nil {
			return result, err
		}
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if options.Action == "check" {
		return installer.check(ctx, result, tools, previous), nil
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return result, err
	}
	release, err := acquireInstallLock(root)
	if err != nil {
		return result, err
	}
	defer release()
	if _, err := os.Stat(filepath.Join(root, ".install-lock")); err == nil {
		return result, failure("legacy_installation_lock_present", 409)
	}
	// Re-read under the writer lock, since another installation may have just completed.
	previous, err = readInstallManifest(root)
	if err != nil {
		return result, err
	}
	if options.Tools == nil && options.Action != "install" && previous.SelectedTools != nil {
		tools = previous.SelectedTools
	}
	manifest := installManifest{SchemaVersion: 1, Tools: map[string]installedTool{}, SelectedTools: append([]string{}, tools...), UpdatedAt: time.Now().UTC()}
	for id, tool := range previous.Tools {
		manifest.Tools[id] = tool
	}
	for _, id := range tools {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		pin := installer.pins[id]
		if tool := previous.Tools[id]; installer.validTool(id, tool.Path) {
			if version, _ := installer.version(ctx, tool.Path); version == pin {
				manifest.Tools[id] = installedTool{Path: tool.Path, Version: pin}
				continue
			}
		}
		binary, err := installer.installTool(ctx, root, id, pin)
		if err != nil {
			return result, err
		}
		manifest.Tools[id] = installedTool{Path: binary, Version: pin}
		result.RestartRequired = true
	}
	verified := installer.check(ctx, result, tools, manifest)
	if !verified.OK {
		return result, failure("installation_verification_failed", 502)
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	temporary, err := os.CreateTemp(root, ".install-manifest-*")
	if err != nil {
		return result, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return result, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return result, err
	}
	if err := temporary.Close(); err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err := os.Rename(temporaryPath, filepath.Join(root, "install.json")); err != nil {
		return result, err
	}
	verified.Command, verified.RestartRequired = options.Action, result.RestartRequired
	return verified, nil
}

func (installer nativeInstaller) validTool(id, binary string) bool {
	if !filepath.IsAbs(binary) {
		return false
	}
	name := id
	if installer.goos == "windows" {
		name += ".exe"
	}
	if !strings.EqualFold(filepath.Base(binary), name) {
		return false
	}
	info, err := os.Stat(binary)
	return err == nil && info.Mode().IsRegular()
}

func (installer nativeInstaller) check(ctx context.Context, result Installation, tools []string, manifest installManifest) Installation {
	result.OK, result.Components = true, []InstallComponent{}
	for _, id := range tools {
		component := InstallComponent{ID: id, Path: manifest.Tools[id].Path, RecommendedVersion: installer.pins[id], Status: "not_installed"}
		if installer.validTool(id, component.Path) {
			component.InstalledVersion, _ = installer.version(ctx, component.Path)
		}
		if component.InstalledVersion != "" {
			component.UpdateAvailable = component.InstalledVersion != component.RecommendedVersion
			component.Status = "current"
			if component.UpdateAvailable {
				component.Status = "update_available"
			}
		}
		result.OK = result.OK && component.Status == "current"
		result.Components = append(result.Components, component)
	}
	return result
}

func readInstallManifest(root string) (installManifest, error) {
	manifest := installManifest{SchemaVersion: 1, Tools: map[string]installedTool{}}
	file, err := os.Open(filepath.Join(root, "install.json"))
	if errors.Is(err, os.ErrNotExist) {
		return manifest, nil
	}
	if err != nil {
		return manifest, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return manifest, failure("invalid_install_manifest", 409)
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != 1 {
		return manifest, failure("invalid_install_manifest", 409)
	}
	if manifest.Tools == nil {
		manifest.Tools = map[string]installedTool{}
	}
	for _, id := range manifest.SelectedTools {
		if id != "opencode" && id != "cline" {
			return manifest, failure("invalid_install_profile", 409)
		}
	}
	return manifest, nil
}

func installRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", failure("absolute_install_directory_required", 400)
	}
	root = filepath.Clean(root)
	ancestor := root
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			root = resolved
			for i := len(suffix) - 1; i >= 0; i-- {
				root = filepath.Join(root, suffix[i])
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", failure("invalid_install_directory", 400)
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = parent
	}
	home, _ := os.UserHomeDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	sameHome := root == home
	if runtime.GOOS == "windows" {
		sameHome = strings.EqualFold(root, home)
	}
	repository, _ := os.ReadFile(filepath.Join(root, "backend", "go.mod"))
	if filepath.Dir(root) == root || sameHome || strings.Contains(string(repository), "module github.com/wooinwoo/freeway-cli/backend") {
		return "", failure("choose_a_dedicated_install_directory", 400)
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return "", failure("invalid_install_directory", 400)
	}
	return root, nil
}

func installPackage(id, goos, goarch string) (string, string, error) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" || (goos != "linux" && goos != "darwin" && goos != "windows") {
		return "", "", failure("unsupported_install_platform", 400)
	}
	name := id
	if goos == "windows" {
		name += ".exe"
	}
	if id == "cline" {
		return "@cline/cli-" + goos + "-" + arch, name, nil
	}
	if id == "opencode" {
		pkg := "opencode-" + goos + "-" + arch
		if arch == "x64" {
			pkg += "-baseline"
		}
		return pkg, name, nil
	}
	return "", "", failure("invalid_install_tools", 400)
}

var installVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var installVersionOutput = regexp.MustCompile(`(?:^|\s)v?(\d+\.\d+\.\d+)(?:\s|$)`)

type installVersionBuffer struct{ bytes.Buffer }

func (buffer *installVersionBuffer) Write(data []byte) (int, error) {
	length := len(data)
	remaining := 65536 - buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.Buffer.Write(data)
	}
	return length, nil
}

func installNativeVersion(ctx context.Context, binary string) (string, error) {
	file, err := os.Open(binary)
	if err != nil {
		return "", failure("native_binary_required", 409)
	}
	var magic [4]byte
	_, err = io.ReadFull(file, magic[:])
	_ = file.Close()
	native := bytes.Equal(magic[:], []byte{0x7f, 'E', 'L', 'F'}) || bytes.Equal(magic[:], []byte{0xcf, 0xfa, 0xed, 0xfe}) || bytes.Equal(magic[:], []byte{0xce, 0xfa, 0xed, 0xfe}) || bytes.Equal(magic[:], []byte{0xca, 0xfe, 0xba, 0xbe}) || bytes.Equal(magic[:], []byte{0xbe, 0xba, 0xfe, 0xca}) || magic[0] == 'M' && magic[1] == 'Z'
	if err != nil || !native {
		return "", failure("native_binary_required", 409)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	var output installVersionBuffer
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	cleanup, err := configureOwnedProcess(cmd)
	if err != nil {
		return "", failure("version_verification_failed", 502)
	}
	defer cleanup()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", failure("version_verification_failed", 502)
	}
	match := installVersionOutput.FindStringSubmatch(output.String())
	if len(match) != 2 {
		return "", failure("version_verification_failed", 502)
	}
	return match[1], nil
}

func installRegistryURL(address *url.URL) bool {
	return address.Scheme == "https" && address.Hostname() == "registry.npmjs.org" && address.Port() == "" && address.User == nil && address.Fragment == ""
}

func (installer nativeInstaller) installTool(ctx context.Context, root, id, pin string) (string, error) {
	pkg, executable, err := installPackage(id, installer.goos, installer.goarch)
	if err != nil {
		return "", err
	}
	prefix := filepath.Join(root, "cli", id+"-"+pin)
	binary := filepath.Join(prefix, "package", "bin", executable)
	var marker struct {
		Package string `json:"package"`
		Version string `json:"version"`
	}
	if data, err := os.ReadFile(filepath.Join(prefix, "package", ".freeway-native.json")); err == nil && json.Unmarshal(data, &marker) == nil && marker.Package == pkg && marker.Version == pin {
		if version, _ := installer.version(ctx, binary); version == pin {
			return binary, nil
		}
	}
	if err := os.Mkdir(filepath.Join(root, "cli"), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if info, err := os.Lstat(filepath.Join(root, "cli")); err != nil || !info.IsDir() {
		return "", failure("unsafe_install_directory", 409)
	}
	stage, err := os.MkdirTemp(filepath.Join(root, "cli"), "."+id+"-"+pin+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	metadataURL := "https://registry.npmjs.org/" + url.PathEscape(pkg) + "/" + pin
	metadataCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	request, _ := http.NewRequestWithContext(metadataCtx, http.MethodGet, metadataURL, nil)
	response, err := installer.client.Do(request)
	if err != nil {
		cancel()
		return "", failure("package_manifest_failed", 502)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	_ = response.Body.Close()
	cancel()
	var metadata struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Dist    struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
		} `json:"dist"`
	}
	if response.StatusCode != 200 || readErr != nil || len(data) > 1024*1024 || json.Unmarshal(data, &metadata) != nil || metadata.Name != pkg || metadata.Version != pin {
		return "", failure("invalid_package_manifest", 502)
	}
	address, err := url.Parse(metadata.Dist.Tarball)
	if err != nil || !installRegistryURL(address) {
		return "", failure("unsafe_package_url", 502)
	}
	var expected []byte
	for _, integrity := range strings.Fields(metadata.Dist.Integrity) {
		if strings.HasPrefix(integrity, "sha512-") {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(integrity, "sha512-"))
			if err == nil && len(decoded) == sha512.Size {
				expected = decoded
				break
			}
		}
	}
	if len(expected) != sha512.Size {
		return "", failure("package_integrity_required", 502)
	}
	archive := filepath.Join(stage, "package.tgz")
	if err := installer.download(ctx, address.String(), archive, expected); err != nil {
		return "", err
	}
	if err := extractNativePackage(ctx, archive, stage, executable); err != nil {
		return "", err
	}
	stagedBinary := filepath.Join(stage, "package", "bin", executable)
	if version, err := installer.version(ctx, stagedBinary); err != nil || version != pin {
		return "", failure("version_verification_failed", 502)
	}
	markerData, _ := json.Marshal(map[string]string{"package": pkg, "version": pin})
	if err := os.WriteFile(filepath.Join(stage, "package", ".freeway-native.json"), markerData, 0600); err != nil {
		return "", err
	}
	prefixInfo, prefixErr := os.Lstat(prefix)
	_, packageErr := os.Lstat(filepath.Join(prefix, "package"))
	if packageErr == nil || prefixErr == nil && !prefixInfo.IsDir() {
		// A damaged or active same-version installation is retained in its existing generation.
		prefix = filepath.Join(root, "cli", strings.TrimPrefix(filepath.Base(stage), "."))
	}
	if err := os.MkdirAll(prefix, 0700); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err := os.Rename(filepath.Join(stage, "package"), filepath.Join(prefix, "package")); err != nil {
		return "", err
	}
	return filepath.Join(prefix, "package", "bin", executable), nil
}

func (installer nativeInstaller) download(ctx context.Context, address, target string, expected []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	response, err := installer.client.Do(request)
	if err != nil {
		return failure("package_download_failed", 502)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return failure("package_download_failed", 502)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha512.New()
	const maximum = 512 * 1024 * 1024
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maximum+1))
	closeErr := file.Close()
	if copyErr != nil || size > maximum {
		return failure("package_download_failed", 502)
	}
	if closeErr != nil {
		return closeErr
	}
	if subtle.ConstantTimeCompare(hash.Sum(nil), expected) != 1 {
		return failure("package_integrity_mismatch", 502)
	}
	return nil
}

type installContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader installContextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}

func extractNativePackage(ctx context.Context, archive, directory, executable string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return failure("invalid_package_archive", 502)
	}
	defer gzipReader.Close()
	const maximum = int64(1024 * 1024 * 1024)
	reader := tar.NewReader(installContextReader{ctx: ctx, reader: io.LimitReader(gzipReader, maximum+1)})
	var total int64
	found := false
	for count := 0; count < 4096; count++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			if found {
				return nil
			}
			return failure("native_binary_missing", 502)
		}
		if err != nil {
			return failure("invalid_package_archive", 502)
		}
		name := path.Clean(header.Name)
		if strings.Contains(header.Name, "\\") || strings.HasPrefix(header.Name, "/") || name != "package" && !strings.HasPrefix(name, "package/") {
			return failure("unsafe_package_archive", 502)
		}
		for _, part := range strings.Split(header.Name, "/") {
			if part == ".." {
				return failure("unsafe_package_archive", 502)
			}
		}
		if header.Size < 0 || header.Size > maximum-total {
			return failure("package_too_large", 502)
		}
		total += header.Size
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return failure("unsafe_package_archive", 502)
		}
		if name != "package/bin/"+executable {
			continue
		}
		if found {
			return failure("unsafe_package_archive", 502)
		}
		found = true
		parent := filepath.Join(directory, "package", "bin")
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
		target, err := os.OpenFile(filepath.Join(parent, executable), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(target, reader)
		if copyErr == nil {
			copyErr = target.Sync()
		}
		closeErr := target.Close()
		if copyErr != nil {
			return failure("invalid_package_archive", 502)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return failure("package_too_large", 502)
}

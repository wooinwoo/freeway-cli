package providers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type installArchiveEntry struct {
	name, body, link string
	kind             byte
}

func installTestArchive(t *testing.T, entries []installArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Typeflag: kind, Linkname: entry.link, Mode: 0755, Size: int64(len(entry.body))}
		if kind == tar.TypeSymlink || kind == tar.TypeLink {
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.WriteString(archive, entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type installTransport func(*http.Request) (*http.Response, error)

func (transport installTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func installFixture(t *testing.T, change func(pkg, version string, archive []byte) ([]byte, string)) (nativeInstaller, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if strings.HasSuffix(r.URL.Path, ".tgz") {
			pkg, version := r.URL.Query().Get("package"), r.URL.Query().Get("version")
			id := "opencode"
			if strings.HasPrefix(pkg, "@cline/") {
				id = "cline"
			}
			_, executable, _ := installPackage(id, runtime.GOOS, runtime.GOARCH)
			archive := installTestArchive(t, []installArchiveEntry{{name: "package/bin/" + executable, body: "fixture-version=" + version}, {name: "package/package.json", body: `{}`}})
			if change != nil {
				archive, _ = change(pkg, version, archive)
			}
			_, _ = w.Write(archive)
			return
		}
		version := parts[len(parts)-1]
		pkg := strings.Join(parts[:len(parts)-1], "/")
		id := "opencode"
		if strings.HasPrefix(pkg, "@cline/") {
			id = "cline"
		}
		_, executable, _ := installPackage(id, runtime.GOOS, runtime.GOARCH)
		archive := installTestArchive(t, []installArchiveEntry{{name: "package/bin/" + executable, body: "fixture-version=" + version}, {name: "package/package.json", body: `{}`}})
		hash := sha512.Sum512(archive)
		integrity := "sha512-" + base64.StdEncoding.EncodeToString(hash[:])
		if change != nil {
			_, replacement := change(pkg, version, archive)
			if replacement != "" {
				integrity = replacement
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": pkg, "version": version, "dist": map[string]string{
			"tarball": "https://registry.npmjs.org/native.tgz?package=" + url.QueryEscape(pkg) + "&version=" + url.QueryEscape(version), "integrity": integrity,
		}})
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	client := &http.Client{Transport: installTransport(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		address := *request.URL
		address.Scheme, address.Host = base.Scheme, base.Host
		clone.URL = &address
		clone.Host = base.Host
		return http.DefaultTransport.RoundTrip(clone)
	})}
	probe := func(_ context.Context, path string) (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(string(data), "fixture-version=") {
			return "", errors.New("invalid fixture executable")
		}
		return strings.TrimPrefix(string(data), "fixture-version="), nil
	}
	return nativeInstaller{client: client, version: probe, goos: runtime.GOOS, goarch: runtime.GOARCH}, requests
}

func TestNativeInstallDirectPackagesArePinnedAndIdempotent(t *testing.T) {
	installer, requests := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	result, err := installer.run(context.Background(), InstallOptions{Root: root})
	if err != nil || !result.OK || !result.RestartRequired || len(result.Components) != 2 || requests.Load() != 4 {
		t.Fatalf("first installation: %+v requests=%d err=%v", result, requests.Load(), err)
	}
	data, err := os.ReadFile(filepath.Join(root, "install.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"node"`) || strings.Contains(string(data), "node_modules") {
		t.Fatal("native installation retained a Node runtime")
	}
	manifest, err := readInstallManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"opencode", "cline"} {
		if !strings.Contains(manifest.Tools[id].Path, filepath.Join("package", "bin")) {
			t.Fatalf("not a native package path: %s", manifest.Tools[id].Path)
		}
	}
	second, err := installer.run(context.Background(), InstallOptions{Action: "update", Root: root})
	if err != nil || !second.OK || second.RestartRequired || requests.Load() != 4 {
		t.Fatalf("idempotent update: %+v requests=%d err=%v", second, requests.Load(), err)
	}
}

func TestNativeInstallCheckDoesNotCreateDirectoriesOrDownload(t *testing.T) {
	installer, requests := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "absent")
	result, err := installer.run(context.Background(), InstallOptions{Action: "check", Root: root})
	if err != nil || result.OK || len(result.Components) != 2 || requests.Load() != 0 {
		t.Fatalf("check: %+v %v", result, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("check created its install directory")
	}
	result, err = installer.run(context.Background(), InstallOptions{Action: "check", Root: root, Tools: []string{}})
	if err != nil || !result.OK || len(result.Components) != 0 {
		t.Fatalf("API-only check: %+v %v", result, err)
	}
}

func TestNativeInstallAPIOnlyProfileSurvivesUpdates(t *testing.T) {
	installer, requests := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	for _, options := range []InstallOptions{{Root: root, Tools: []string{}}, {Root: root, Action: "update"}, {Root: root, Action: "check"}} {
		result, err := installer.run(context.Background(), options)
		if err != nil || !result.OK || len(result.Components) != 0 || requests.Load() != 0 {
			t.Fatalf("API-only: %+v %v", result, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("API-only profile installed vendor software")
	}
}

func TestNativeInstallUpdateRetainsOlderBinaries(t *testing.T) {
	installer, _ := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	first, err := installer.run(context.Background(), InstallOptions{Root: root, Tools: []string{"opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	previous := first.Components[0].Path
	installer.pins = map[string]string{"opencode": "1.18.36", "cline": "3.0.62"}
	updated, err := installer.run(context.Background(), InstallOptions{Action: "update", Root: root})
	if err != nil || !updated.OK || !updated.RestartRequired || len(updated.Components) != 1 || updated.Components[0].Path == previous {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := os.Stat(previous); err != nil {
		t.Fatal("previous installation was removed")
	}
}

func TestNativeInstallFailurePreservesManifestAndCleansStage(t *testing.T) {
	installer, _ := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	if _, err := installer.run(context.Background(), InstallOptions{Root: root, Tools: []string{"opencode"}}); err != nil {
		t.Fatal(err)
	}
	manifest, _ := os.ReadFile(filepath.Join(root, "install.json"))
	broken, _ := installFixture(t, func(pkg, version string, archive []byte) ([]byte, string) { return append(archive, 'x'), "" })
	broken.pins = map[string]string{"opencode": "1.18.36", "cline": "3.0.62"}
	if _, err := broken.run(context.Background(), InstallOptions{Action: "update", Root: root}); err == nil {
		t.Fatal("corrupt package was accepted")
	}
	current, _ := os.ReadFile(filepath.Join(root, "install.json"))
	if !bytes.Equal(current, manifest) {
		t.Fatal("failed update replaced the active manifest")
	}
	stages, _ := filepath.Glob(filepath.Join(root, "cli", ".*"))
	if len(stages) != 0 {
		t.Fatalf("partial stages remain: %v", stages)
	}
	if release, err := acquireInstallLock(root); err != nil {
		t.Fatal("failed update retained the installation lock")
	} else {
		release()
	}
}

func TestNativeInstallFinalVerificationPrecedesManifestPublication(t *testing.T) {
	installer, _ := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	probe := installer.version
	installer.version = func(ctx context.Context, path string) (string, error) {
		if !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path)))), ".") {
			return "", errors.New("published executable failed validation")
		}
		return probe(ctx, path)
	}
	if _, err := installer.run(context.Background(), InstallOptions{Root: root, Tools: []string{"opencode"}}); err == nil {
		t.Fatal("failed final verification was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "install.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("candidate manifest was published before final verification")
	}
}

func TestNativeInstallRejectsMissingIntegrity(t *testing.T) {
	installer, requests := installFixture(t, func(pkg, version string, archive []byte) ([]byte, string) { return archive, "sha1-not-a-sha512-digest" })
	_, err := installer.run(context.Background(), InstallOptions{Root: filepath.Join(t.TempDir(), "managed"), Tools: []string{"opencode"}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "package_integrity_required" || requests.Load() != 1 {
		t.Fatalf("integrity: requests=%d err=%v", requests.Load(), err)
	}
}

func TestNativeInstallRejectsMalformedSHA512EvenWithAValidDigestPrefix(t *testing.T) {
	installer, requests := installFixture(t, func(pkg, version string, archive []byte) ([]byte, string) {
		hash := sha512.Sum512(archive)
		return archive, "sha512-" + base64.StdEncoding.EncodeToString(hash[:]) + "!"
	})
	_, err := installer.run(context.Background(), InstallOptions{Root: filepath.Join(t.TempDir(), "managed"), Tools: []string{"opencode"}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "package_integrity_required" || requests.Load() != 1 {
		t.Fatalf("malformed integrity: requests=%d err=%v", requests.Load(), err)
	}
}

func TestNativeInstallArchiveRejectsLinksTraversalAndDuplicates(t *testing.T) {
	for name, entries := range map[string][]installArchiveEntry{
		"traversal":    {{name: "package/../escape", body: "outside"}},
		"absolute":     {{name: "/tmp/escape", body: "outside"}},
		"symlink":      {{name: "package/bin/opencode", kind: tar.TypeSymlink, link: "/tmp/escape"}},
		"hardlink":     {{name: "package/bin/opencode", kind: tar.TypeLink, link: "/tmp/escape"}},
		"duplicate":    {{name: "package/bin/opencode", body: "a"}, {name: "package/bin/opencode", body: "b"}},
		"windows-path": {{name: `package\bin\opencode`, body: "outside"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "fixture.tgz")
			if err := os.WriteFile(archive, installTestArchive(t, entries), 0600); err != nil {
				t.Fatal(err)
			}
			if err := extractNativePackage(context.Background(), archive, filepath.Join(root, "stage"), "opencode"); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestNativeInstallRetainsDamagedSameVersionGeneration(t *testing.T) {
	installer, _ := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	first, err := installer.run(context.Background(), InstallOptions{Root: root, Tools: []string{"opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	path := first.Components[0].Path
	if err := os.WriteFile(path, []byte("damaged existing binary"), 0700); err != nil {
		t.Fatal(err)
	}
	second, err := installer.run(context.Background(), InstallOptions{Action: "update", Root: root})
	if err != nil || !second.OK || second.Components[0].Path == path {
		t.Fatalf("repair: %+v %v", second, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "damaged existing binary" {
		t.Fatal("repair overwrote the retained generation")
	}
}

func TestNativeInstallChecksLegacyNativePathsWithoutRunningNode(t *testing.T) {
	installer, requests := installFixture(t, nil)
	root := t.TempDir()
	_, name, _ := installPackage("opencode", runtime.GOOS, runtime.GOARCH)
	binary := filepath.Join(root, "node_modules", "opencode-native", "bin", name)
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("fixture-version=1.18.35"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"schemaVersion": 1, "node": map[string]string{"path": "never-run-node"}, "tools": map[string]any{"opencode": map[string]string{"path": binary, "version": "1.18.35"}}, "selectedTools": []string{"opencode"}})
	if err := os.WriteFile(filepath.Join(root, "install.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := installer.run(context.Background(), InstallOptions{Action: "check", Root: root})
	if err != nil || !result.OK || len(result.Components) != 1 || requests.Load() != 0 {
		t.Fatalf("legacy native check: %+v %v", result, err)
	}
	current, _ := os.ReadFile(filepath.Join(root, "install.json"))
	if !bytes.Equal(data, current) {
		t.Fatal("check changed the legacy manifest")
	}
}

func TestNativeInstallRejectsUnsafeRootsAndLiveWriterLocks(t *testing.T) {
	installer, _ := installFixture(t, nil)
	home, _ := os.UserHomeDir()
	for _, root := range []string{filepath.VolumeName(home) + string(filepath.Separator), home, "relative/install"} {
		if _, err := installer.run(context.Background(), InstallOptions{Root: root}); err == nil {
			t.Fatalf("unsafe root accepted: %s", root)
		}
	}
	root := t.TempDir()
	release, err := acquireInstallLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := installer.run(context.Background(), InstallOptions{Root: root}); err == nil {
		t.Fatal("second installation ignored the writer lock")
	}
}

func TestNativeInstallCancellationCleansItsTemporaryFiles(t *testing.T) {
	installer, _ := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	started := make(chan struct{})
	installer.client = &http.Client{Transport: installTransport(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := installer.run(ctx, InstallOptions{Root: root, Tools: []string{"opencode"}})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata request did not start")
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	stages, _ := filepath.Glob(filepath.Join(root, "cli", ".*"))
	if len(stages) != 0 {
		t.Fatalf("cancelled install left stages: %v", stages)
	}
	if _, err := os.Stat(filepath.Join(root, "install.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled install published a manifest")
	}
}

func TestNativeInstallNativeProbeRejectsInterpreterScripts(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	script := filepath.Join(root, "opencode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho invalid > "+marker+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := installNativeVersion(context.Background(), script); err == nil {
		t.Fatal("interpreter script was executed as a native CLI")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the installer ran a shell or Node-style script")
	}
}

func TestNativeInstallPlatformPackagesAndRegistryOrigin(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			for _, id := range []string{"opencode", "cline"} {
				pkg, executable, err := installPackage(id, goos, arch)
				if err != nil || !strings.Contains(pkg, goos) || strings.HasSuffix(executable, ".exe") != (goos == "windows") {
					t.Fatalf("platform %s/%s %s: %s %s %v", goos, arch, id, pkg, executable, err)
				}
			}
		}
	}
	for _, address := range []string{"http://registry.npmjs.org/file.tgz", "https://evil.example/file.tgz", "https://user:secret@registry.npmjs.org/file.tgz", "https://registry.npmjs.org:444/file.tgz"} {
		parsed, _ := url.Parse(address)
		if installRegistryURL(parsed) {
			t.Fatalf("unsafe registry origin accepted: %s", address)
		}
	}
	if _, _, err := installPackage("opencode", "linux", "386"); err == nil {
		t.Fatal("unsupported architecture was accepted")
	}
}

func TestNativeInstallDoesNotFollowPrefixSymlinks(t *testing.T) {
	installer, _ := installFixture(t, nil)
	root := filepath.Join(t.TempDir(), "managed")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cli"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "cli", "opencode-1.18.35")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	result, err := installer.run(context.Background(), InstallOptions{Root: root, Tools: []string{"opencode"}})
	if err != nil || !result.OK {
		t.Fatalf("safe generation: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "package")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("installation escaped through an existing prefix symlink")
	}
}

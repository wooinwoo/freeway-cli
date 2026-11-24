package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The child process is an actual native executable, with a version determined
// by its package. This exercises version probes without invoking a shell.
func TestMain(m *testing.M) {
	if os.Getenv("FREEWAY_UPDATE_TEST_HELPER") == "1" && len(os.Args) == 2 && os.Args[1] == "--version" {
		self, _ := os.Executable()
		data, _ := os.ReadFile(filepath.Join(filepath.Dir(self), "manifest.json"))
		var p Package
		if json.Unmarshal(data, &p) != nil {
			os.Exit(2)
		}
		fmt.Println(p.Version)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func archiveFixture(t *testing.T, version string, change func(map[string][]byte)) ([]byte, string) {
	t.Helper()
	t.Setenv("FREEWAY_UPDATE_TEST_HELPER", "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{exe("freeway"): binary, exe("freeway-providers"): binary, "LICENSE": []byte("test license"), "THIRD_PARTY_NOTICES.md": []byte("test notices")}
	p := Package{SchemaVersion: 1, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, APIVersion: 1, Files: map[string]string{}}
	for name, data := range files {
		p.Files[name] = checksum(data)
	}
	files["manifest.json"], _ = json.Marshal(p)
	if change != nil {
		change(files)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), checksum(out.Bytes())
}

func TestInstallUpdateRollbackAndUninstallPreserveData(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	accounts := filepath.Join(base, "accounts.json")
	if err := os.WriteFile(accounts, []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{Root: filepath.Join(base, "app")}
	a, sha := archiveFixture(t, "1.0.0", nil)
	s, err := m.install(ctx, a, sha, "1.0.0")
	if err != nil || s.Active != "1.0.0" || s.Previous != "" {
		t.Fatalf("clean install: %+v %v", s, err)
	}
	b, sha := archiveFixture(t, "1.1.0", nil)
	s, err = m.install(ctx, b, sha, "1.1.0")
	if err != nil || s.Active != "1.1.0" || s.Previous != "1.0.0" {
		t.Fatalf("upgrade: %+v %v", s, err)
	}
	s, err = m.Rollback(ctx)
	if err != nil || s.Active != "1.0.0" || s.Previous != "1.1.0" {
		t.Fatalf("rollback: %+v %v", s, err)
	}
	if runtime.GOOS != "windows" {
		if _, err = m.Uninstall(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(m.Root, "install.json")); !os.IsNotExist(err) {
			t.Fatalf("installation still present: %v", err)
		}
		if data, err := os.ReadFile(accounts); err != nil || string(data) != "user data" {
			t.Fatalf("user data removed: %s %v", data, err)
		}
	}
}

func TestFailedUpdatesKeepActiveVersion(t *testing.T) {
	m := Manager{Root: filepath.Join(t.TempDir(), "app")}
	ctx := context.Background()
	a, sha := archiveFixture(t, "1.0.0", nil)
	if _, err := m.install(ctx, a, sha, ""); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		change   func(map[string][]byte)
		expected string
		badHash  bool
	}{
		{name: "checksum", badHash: true},
		{name: "version disagreement", expected: "9.0.0"},
		{name: "traversal", change: func(f map[string][]byte) { f["../escape"] = []byte("bad") }},
		{name: "unlisted license", change: func(f map[string][]byte) { f["licenses/unlisted"] = []byte("bad") }},
		{name: "tampered binary", change: func(f map[string][]byte) { f[exe("freeway")] = []byte("bad") }},
		{name: "incompatible platform", change: func(f map[string][]byte) {
			var p Package
			json.Unmarshal(f["manifest.json"], &p)
			p.OS = "other"
			f["manifest.json"], _ = json.Marshal(p)
		}},
		{name: "broken native executable", change: func(f map[string][]byte) {
			var p Package
			json.Unmarshal(f["manifest.json"], &p)
			f[exe("freeway")] = []byte("not executable")
			p.Files[exe("freeway")] = checksum(f[exe("freeway")])
			f["manifest.json"], _ = json.Marshal(p)
		}},
		{name: "immutable version", change: func(f map[string][]byte) {
			var p Package
			json.Unmarshal(f["manifest.json"], &p)
			p.Version = "1.0.0"
			f["LICENSE"] = []byte("changed")
			p.Files["LICENSE"] = checksum(f["LICENSE"])
			f["manifest.json"], _ = json.Marshal(p)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, hash := archiveFixture(t, "1.1.0", tc.change)
			if tc.badHash {
				hash = checksum([]byte("wrong"))
			}
			if _, err := m.install(ctx, data, hash, tc.expected); err == nil {
				t.Fatal("unsafe update succeeded")
			}
			s, err := m.Status()
			if err != nil || s.Active != "1.0.0" {
				t.Fatalf("failed update changed state: %+v %v", s, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.install(ctx, a, sha, ""); err != context.Canceled {
		t.Fatalf("canceled update accepted: %v", err)
	}
}

func TestUpdateSkipsEqualVersionAndRefusesOlderRelease(t *testing.T) {
	m := Manager{Root: filepath.Join(t.TempDir(), "app")}
	ctx := t.Context()
	archive, sha := archiveFixture(t, "1.0.0-rc.10", nil)
	if _, err := m.install(ctx, archive, sha, "1.0.0-rc.10"); err != nil {
		t.Fatal(err)
	}
	for _, advertised := range []string{"1.0.0-rc.9", "1.0.0-rc.10"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/release.json" {
				t.Error("update downloaded an archive without a newer version")
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(Release{SchemaVersion: 1, Version: advertised, Artifacts: []Artifact{
				{OS: runtime.GOOS, Arch: runtime.GOARCH, SHA256: sha, URL: "http://" + r.Host + "/archive"},
			}})
		}))
		m.ReleaseURL = server.URL + "/release.json"
		state, err := m.Update(ctx)
		server.Close()
		if (err != nil) != (advertised == "1.0.0-rc.9") || state.Active != "1.0.0-rc.10" {
			t.Fatalf("unexpected update result for %s: %+v %v", advertised, state, err)
		}
	}
	if compareVersions("1.0.0", "1.0.0-rc.10") <= 0 {
		t.Fatal("stable release should follow its prerelease")
	}
}

func TestDamagedVersionRepairsFromUpdateAndVerifiedArchive(t *testing.T) {
	const version = "1.2.3"
	m := Manager{Root: filepath.Join(t.TempDir(), "app")}
	content, hash := archiveFixture(t, version, nil)
	archive := filepath.Join(t.TempDir(), "freeway.zip")
	if err := os.WriteFile(archive, content, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(t.Context(), archive, hash); err != nil {
		t.Fatal(err)
	}
	versionDir := filepath.Join(m.Root, "versions", version)
	utility := filepath.Join(versionDir, exe("freeway-providers"))
	damage := func() {
		t.Helper()
		if err := os.WriteFile(utility, []byte("corrupt fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release.json":
			_ = json.NewEncoder(w).Encode(Release{SchemaVersion: 1, Version: version, Artifacts: []Artifact{
				{OS: runtime.GOOS, Arch: runtime.GOARCH, SHA256: hash, URL: "http://" + r.Host + "/archive"},
			}})
		case "/archive":
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m.ReleaseURL = server.URL + "/release.json"
	damage()
	if state, err := m.Update(t.Context()); err != nil || state.Active != version || state.Previous != "" {
		t.Fatalf("same-version update: %+v %v", state, err)
	}
	if _, err := validatePackage(versionDir, version); err != nil {
		t.Fatal(err)
	}
	damage()
	if _, err := m.Install(t.Context(), archive, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := validatePackage(versionDir, version); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveRejectsLinksAndDuplicateEntries(t *testing.T) {
	var zipped bytes.Buffer
	z := zip.NewWriter(&zipped)
	for range 2 {
		f, _ := z.Create("LICENSE")
		f.Write([]byte("license"))
	}
	z.Close()
	if err := extract(zipped.Bytes(), t.TempDir()); err == nil {
		t.Fatal("duplicate zip entry accepted")
	}
	var packed bytes.Buffer
	g := gzip.NewWriter(&packed)
	tarball := tar.NewWriter(g)
	tarball.WriteHeader(&tar.Header{Name: "LICENSE", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	tarball.Close()
	g.Close()
	if err := extract(packed.Bytes(), t.TempDir()); err == nil {
		t.Fatal("symlink tar entry accepted")
	}
}

func TestRemoteUpdateVerifiesManifestAndNativeArtifact(t *testing.T) {
	a, hash := archiveFixture(t, "2.0.0", nil)
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/archive.zip" {
			w.Write(a)
			return
		}
		json.NewEncoder(w).Encode(Release{SchemaVersion: 1, Version: "2.0.0", Artifacts: []Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: base + "/archive.zip", SHA256: hash}}})
	}))
	defer server.Close()
	base = server.URL
	m := Manager{Root: filepath.Join(t.TempDir(), "app"), ReleaseURL: base + "/release.json"}
	if s, err := m.Update(context.Background()); err != nil || s.Active != "2.0.0" {
		t.Fatalf("remote update failed: %+v %v", s, err)
	}
	for _, address := range []string{"http://example.com/file", "file:///tmp/archive", "https://user:pass@example.com/file"} {
		if allowedURL(address) == nil {
			t.Fatalf("unsafe download allowed: %s", address)
		}
	}
}

func TestInstallerRejectsConcurrentOperationAndSymlinkRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	os.MkdirAll(root, 0700)
	release, err := lock(filepath.Join(root, ".update.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	a, sha := archiveFixture(t, "1.0.0", nil)
	if _, err = (Manager{Root: root}).install(context.Background(), a, sha, ""); err == nil {
		t.Fatal("concurrent operation accepted")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "app")
		if err = os.Symlink(root, link); err != nil {
			t.Fatal(err)
		}
		if _, err = (Manager{Root: link}).root(); err == nil {
			t.Fatal("symlink root accepted")
		}
	}
}

func TestActivePackageStampSkipsHashingOnlyWhileFilesAreUnchanged(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "versions", "1.2.3")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte("fixture binary")
	files := map[string]string{}
	for _, name := range []string{exe("freeway"), exe("freeway-providers"), exe("freewayd"), "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		os.WriteFile(filepath.Join(directory, name), body, 0o700)
		files[name] = checksum(body)
	}
	manifest, _ := json.Marshal(Package{SchemaVersion: 1, Version: "1.2.3", OS: runtime.GOOS, Arch: runtime.GOARCH, APIVersion: 1, Files: files})
	os.WriteFile(filepath.Join(directory, "manifest.json"), manifest, 0o600)
	if err := validateActive(root, directory, "1.2.3"); err != nil {
		t.Fatalf("a valid package was rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "verified.json")); err != nil {
		t.Fatalf("the verified stamp was not written: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, exe("freeway")), []byte("damaged binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateActive(root, directory, "1.2.3"); err == nil {
		t.Fatal("a changed file passed on a stale stamp")
	}
	os.WriteFile(filepath.Join(directory, exe("freeway")), body, 0o700)
	if err := validateActive(root, directory, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "planted.dll"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateActive(root, directory, "1.2.3"); err == nil {
		t.Fatal("an unlisted file passed on the fast path")
	}
}

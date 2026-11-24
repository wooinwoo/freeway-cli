// Package update installs immutable native client versions without modifying user data.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
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

const DefaultReleaseURL = "https://github.com/wooinwoo/freeway-cli/releases/latest/download/release.json"
const MaxArchive = 256 * 1024 * 1024

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

type Package struct {
	SchemaVersion int               `json:"schemaVersion"`
	Version       string            `json:"version"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	APIVersion    int               `json:"apiVersion"`
	Files         map[string]string `json:"files"`
}
type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type Release struct {
	SchemaVersion int        `json:"schemaVersion"`
	Version       string     `json:"version"`
	Artifacts     []Artifact `json:"artifacts"`
}
type State struct {
	Product       string `json:"product"`
	SchemaVersion int    `json:"schemaVersion"`
	Active        string `json:"active"`
	Previous      string `json:"previous,omitempty"`
	Removing      bool   `json:"removing,omitempty"`
}
type Manager struct {
	Root       string
	ReleaseURL string
	HTTPClient *http.Client
}

func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".freeway", "app"), nil
}

func (m Manager) root() (string, error) {
	root := m.Root
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return "", err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	if root == filepath.VolumeName(root)+string(filepath.Separator) || root == filepath.Clean(home) {
		return "", errors.New("choose a dedicated application directory")
	}
	for _, item := range []string{root, filepath.Join(root, "versions"), filepath.Join(root, "bin")} {
		info, err := os.Lstat(item)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", fmt.Errorf("unsafe installation directory: %s", item)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return root, nil
}

func (m Manager) Status() (State, error) {
	root, err := m.root()
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(filepath.Join(root, "install.json"))
	if err != nil {
		return State{}, err
	}
	var s State
	if json.Unmarshal(data, &s) != nil || s.Product != "freeway" || s.SchemaVersion != 1 || !versionPattern.MatchString(s.Active) || (s.Previous != "" && !versionPattern.MatchString(s.Previous)) {
		return State{}, errors.New("invalid installation manifest")
	}
	return s, nil
}

func allowedURL(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.User != nil || u.Host == "" || u.Fragment != "" {
		return errors.New("invalid release URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()) {
		return nil
	}
	return errors.New("release downloads require HTTPS")
}

func (m Manager) fetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	if err := allowedURL(address); err != nil {
		return nil, err
	}
	client := m.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many download redirects")
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("insecure download redirect")
		}
		return allowedURL(req.URL.String())
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("release download failed (HTTP %d); no installed version was changed", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("release download exceeds size limit")
	}
	return data, nil
}

func (m Manager) Check(ctx context.Context) (Release, error) {
	address := m.ReleaseURL
	if address == "" {
		address = DefaultReleaseURL
	}
	data, err := m.fetch(ctx, address, 1024*1024)
	if err != nil {
		return Release{}, err
	}
	var release Release
	if json.Unmarshal(data, &release) != nil || release.SchemaVersion != 1 || !versionPattern.MatchString(release.Version) {
		return Release{}, errors.New("invalid release manifest")
	}
	for _, artifact := range release.Artifacts {
		if artifact.OS == runtime.GOOS && artifact.Arch == runtime.GOARCH {
			if !validHash(artifact.SHA256) {
				return Release{}, errors.New("release checksum is missing or invalid")
			}
			if err := allowedURL(artifact.URL); err != nil {
				return Release{}, err
			}
			return release, nil
		}
	}
	return Release{}, fmt.Errorf("no native archive is available for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func (m Manager) Update(ctx context.Context) (State, error) {
	release, err := m.Check(ctx)
	if err != nil {
		return State{}, err
	}
	return m.InstallRelease(ctx, release)
}

func (m Manager) InstallRelease(ctx context.Context, release Release) (State, error) {
	state, err := m.Status()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}
	if unchanged, err := m.checkUpdate(state, release.Version); unchanged || err != nil {
		return state, err
	}
	for _, artifact := range release.Artifacts {
		if artifact.OS != runtime.GOOS || artifact.Arch != runtime.GOARCH {
			continue
		}
		data, err := m.fetch(ctx, artifact.URL, MaxArchive)
		if err != nil {
			return State{}, err
		}
		return m.installVersion(ctx, data, artifact.SHA256, release.Version, true)
	}
	return State{}, errors.New("native release artifact not found")
}

func (m Manager) checkUpdate(state State, version string) (bool, error) {
	same, err := updateVersion(state, version)
	if !same || err != nil {
		return false, err
	}
	root, err := m.root()
	if err != nil {
		return false, err
	}
	_, err = validatePackage(filepath.Join(root, "versions", state.Active), state.Active)
	return err == nil, nil
}

func (m Manager) Install(ctx context.Context, archive, checksum string) (State, error) {
	f, err := os.Open(archive)
	if err != nil {
		return State{}, err
	}
	defer f.Close()
	return m.installArchive(ctx, f, checksum, "")
}

// InstallBundle installs the bundled release while preserving a newer installation.
func (m Manager) InstallBundle(ctx context.Context, archive io.Reader, checksum, version string) (State, error) {
	if !versionPattern.MatchString(version) {
		return State{}, errors.New("invalid setup version")
	}
	return m.installArchive(ctx, archive, checksum, version)
}

func (m Manager) installArchive(ctx context.Context, archive io.Reader, checksum, version string) (State, error) {
	data, err := io.ReadAll(io.LimitReader(archive, MaxArchive+1))
	if err != nil {
		return State{}, err
	}
	if len(data) > MaxArchive {
		return State{}, errors.New("archive exceeds size limit")
	}
	return m.install(ctx, data, checksum, version)
}

func validHash(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size
}
func checksum(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func safeName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\:") || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") {
		return false
	}
	return name == "manifest.json" || programFile(name) || name == "LICENSE" || name == "THIRD_PARTY_NOTICES.md" || strings.HasPrefix(name, "licenses/")
}

func programFile(name string) bool {
	return name == exe("freeway") || name == exe("freeway-providers") || name == exe("freewayd")
}

func extract(data []byte, directory string) error {
	seen := map[string]bool{}
	var total int64
	write := func(name string, mode os.FileMode, size int64, r io.Reader) error {
		if !safeName(name) || !mode.IsRegular() || size < 0 || size > MaxArchive || seen[name] {
			return fmt.Errorf("unsafe archive entry: %s", name)
		}
		seen[name] = true
		total += size
		if total > MaxArchive || len(seen) > 1000 {
			return errors.New("unpacked archive exceeds size limit")
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		permissions := os.FileMode(0600)
		if programFile(name) {
			permissions = 0700
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, permissions)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(f, io.LimitReader(r, size+1))
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != size {
			return errors.New("archive entry size mismatch")
		}
		return nil
	}
	if bytesZip(data) {
		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, entry := range r.File {
			f, err := entry.Open()
			if err != nil {
				return err
			}
			err = write(entry.Name, entry.Mode(), int64(entry.UncompressedSize64), f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return errors.New("archive must be .zip or .tar.gz")
	}
	defer gz.Close()
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("non-regular archive entry: %s", h.Name)
		}
		if err := write(h.Name, h.FileInfo().Mode(), h.Size, r); err != nil {
			return err
		}
	}
}
func bytesZip(data []byte) bool { return len(data) >= 4 && string(data[:4]) == "PK\x03\x04" }

func readPackageManifest(directory, version string) (Package, error) {
	var p Package
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return p, err
	}
	if json.Unmarshal(data, &p) != nil || p.SchemaVersion != 1 || !versionPattern.MatchString(p.Version) || p.OS != runtime.GOOS || p.Arch != runtime.GOARCH || p.APIVersion != 1 || len(p.Files) == 0 {
		return p, errors.New("incompatible native package manifest")
	}
	if version != "" && p.Version != version {
		return p, errors.New("release and archive versions disagree")
	}
	for _, required := range []string{exe("freeway"), exe("freeway-providers"), "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		if !validHash(p.Files[required]) {
			return p, fmt.Errorf("package is missing %s", required)
		}
	}
	for name, want := range p.Files {
		if !safeName(name) || name == "manifest.json" || !validHash(want) {
			return p, errors.New("invalid package file manifest")
		}
	}
	return p, nil
}

func validatePackage(directory, version string) (Package, error) {
	p, err := readPackageManifest(directory, version)
	if err != nil {
		return p, err
	}
	for name, want := range p.Files {
		file := filepath.Join(directory, filepath.FromSlash(name))
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() {
			return p, fmt.Errorf("package file is missing or unsafe: %s", name)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return p, err
		}
		if checksum(data) != strings.ToLower(want) {
			return p, fmt.Errorf("package checksum mismatch: %s", name)
		}
	}
	err = filepath.WalkDir(directory, func(file string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(directory, file)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name != "manifest.json" && p.Files[name] == "" {
			return fmt.Errorf("unlisted package file: %s", name)
		}
		return nil
	})
	return p, err
}

func probe(ctx context.Context, directory string, p Package) error {
	names := []string{"freeway", "freeway-providers"}
	// Pre-gateway versions remain usable as rollback targets.
	if p.Files[exe("freewayd")] != "" {
		names = append(names, "freewayd")
	}
	for _, name := range names {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		cmd := exec.CommandContext(ctx, filepath.Join(directory, exe(name)), "--version")
		hideVersionProbe(cmd)
		out, err := cmd.Output()
		cancel()
		if err != nil || strings.TrimSpace(string(out)) != p.Version {
			return fmt.Errorf("%s did not pass native version verification", name)
		}
	}
	return nil
}

func (m Manager) install(ctx context.Context, data []byte, want, version string) (State, error) {
	return m.installVersion(ctx, data, want, version, false)
}

func (m Manager) installVersion(ctx context.Context, data []byte, want, version string, automatic bool) (_ State, installErr error) {
	if !validHash(want) || checksum(data) != strings.ToLower(want) {
		return State{}, errors.New("archive SHA256 mismatch; existing installation was not changed")
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	root, err := m.root()
	if err != nil {
		return State{}, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return State{}, err
	}
	release, err := lock(filepath.Join(root, ".update.lock"))
	if err != nil {
		return State{}, err
	}
	defer release()
	state, stateErr := m.Status()
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return State{}, stateErr
	}
	if state.Removing {
		return State{}, errors.New("installation removal is in progress")
	}
	if automatic {
		if unchanged, err := m.checkUpdate(state, version); unchanged || err != nil {
			return state, err
		}
	}
	versions := filepath.Join(root, "versions")
	if err = os.MkdirAll(versions, 0700); err != nil {
		return State{}, err
	}
	stage, err := os.MkdirTemp(versions, ".stage-")
	if err != nil {
		return State{}, err
	}
	defer os.RemoveAll(stage)
	if err = extract(data, stage); err != nil {
		return State{}, err
	}
	p, err := validatePackage(stage, version)
	if err != nil {
		return State{}, err
	}
	if !automatic && version != "" {
		if _, err := updateVersion(state, version); err != nil {
			return state, err
		}
	}
	target := filepath.Join(versions, p.Version)
	published := false
	backup := ""
	defer func() {
		if backup == "" {
			return
		}
		if installErr != nil {
			if published {
				if err := os.Rename(target, stage); err != nil {
					installErr = errors.Join(installErr, fmt.Errorf("restore package from %s: %w", backup, err))
					return
				}
			}
			if err := os.Rename(filepath.Join(backup, "package"), target); err != nil {
				installErr = errors.Join(installErr, fmt.Errorf("restore package from %s: %w", backup, err))
				return
			}
		}
		_ = os.RemoveAll(backup)
	}()
	if info, statErr := os.Lstat(target); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return State{}, errors.New("unsafe existing version directory")
		}
		existing, validErr := validatePackage(target, p.Version)
		var manifestErr error
		if validErr != nil {
			existing, manifestErr = readPackageManifest(target, p.Version)
		}
		if manifestErr == nil && !maps.Equal(existing.Files, p.Files) {
			return State{}, errors.New("an immutable version already exists with different content")
		}
		if validErr != nil {
			saved, err := os.MkdirTemp(versions, ".repair-")
			if err != nil {
				return State{}, err
			}
			if err := os.Rename(target, filepath.Join(saved, "package")); err != nil {
				_ = os.Remove(saved)
				return State{}, fmt.Errorf("close Freeway to repair the installed version: %w", err)
			}
			backup = saved
			if err := os.Rename(stage, target); err != nil {
				return State{}, err
			}
			published = true
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return State{}, statErr
	} else if err = os.Rename(stage, target); err != nil {
		return State{}, err
	} else {
		published = true
	}
	// Windows can hold a directory open after an executable inside it has run.
	// Start version probes only after the directory reaches its final path.
	if err = probe(ctx, target, p); err != nil {
		if published && backup == "" {
			err = errors.Join(err, os.RemoveAll(target))
		}
		return State{}, err
	}
	bin := filepath.Join(root, "bin")
	if err = os.MkdirAll(bin, 0700); err != nil {
		return State{}, err
	}
	launcher := filepath.Join(bin, exe("freeway"))
	info, err := os.Lstat(launcher)
	replaceLauncher := errors.Is(err, os.ErrNotExist)
	if err != nil && !replaceLauncher {
		return State{}, err
	}
	if !replaceLauncher && !info.Mode().IsRegular() {
		return State{}, errors.New("unsafe existing launcher")
	}
	if !replaceLauncher && stateErr != nil {
		contents, readErr := os.ReadFile(launcher)
		if readErr != nil || checksum(contents) != p.Files[exe("freeway")] {
			return State{}, errors.New("refusing to replace an unmanaged launcher")
		}
	}
	if !replaceLauncher && stateErr == nil && !automatic && compareVersions(p.Version, state.Active) >= 0 {
		// A package bootstrap can refresh an older launcher. The running
		// launcher remains usable as a recovery entry point during updates.
		self, selfErr := os.Executable()
		if selfErr != nil {
			return State{}, selfErr
		}
		running, statErr := os.Stat(self)
		if statErr != nil {
			return State{}, statErr
		}
		if !os.SameFile(info, running) {
			contents, readErr := os.ReadFile(launcher)
			replaceLauncher = readErr != nil || checksum(contents) != p.Files[exe("freeway")]
		}
	}
	if replaceLauncher {
		contents, readErr := os.ReadFile(filepath.Join(target, exe("freeway")))
		if readErr != nil {
			return State{}, readErr
		}
		if err := atomicWrite(launcher, contents, 0700); err != nil {
			return State{}, fmt.Errorf("refresh launcher with Freeway closed: %w", err)
		}
	}
	if err = ctx.Err(); err != nil {
		return State{}, err
	}
	previous := state.Active
	if previous == p.Version {
		previous = state.Previous
	}
	next := State{Product: "freeway", SchemaVersion: 1, Active: p.Version, Previous: previous}
	encoded, _ := json.MarshalIndent(next, "", "  ")
	if err = atomicWrite(filepath.Join(root, "install.json"), encoded, 0600); err != nil {
		return State{}, err
	}
	return next, nil
}

func atomicWrite(target string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".freeway-write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), target)
}

func (m Manager) Rollback(ctx context.Context) (State, error) {
	root, err := m.root()
	if err != nil {
		return State{}, err
	}
	release, err := lock(filepath.Join(root, ".update.lock"))
	if err != nil {
		return State{}, err
	}
	defer release()
	s, err := m.Status()
	if err != nil {
		return State{}, err
	}
	if s.Removing {
		return State{}, errors.New("installation removal is in progress")
	}
	if s.Previous == "" {
		return State{}, errors.New("no previous version is available")
	}
	directory := filepath.Join(root, "versions", s.Previous)
	p, err := validatePackage(directory, s.Previous)
	if err != nil {
		return State{}, err
	}
	if err = probe(ctx, directory, p); err != nil {
		return State{}, err
	}
	s.Active, s.Previous = s.Previous, s.Active
	data, _ := json.MarshalIndent(s, "", "  ")
	if err = atomicWrite(filepath.Join(root, "install.json"), data, 0600); err != nil {
		return State{}, err
	}
	return s, nil
}

// Forward runs the immutable active binary from the stable installed launcher.
func Forward(args []string) (bool, int, error) {
	self, err := os.Executable()
	if err != nil {
		return false, 0, err
	}
	if filepath.Base(filepath.Dir(self)) != "bin" {
		return false, 0, nil
	}
	m := Manager{Root: filepath.Dir(filepath.Dir(self))}
	if len(args) > 0 {
		switch args[0] {
		case "install", "update", "rollback", "uninstall":
			// Recovery must remain available when the active binary is damaged.
			if err := os.Setenv("FREEWAY_INSTALL_ROOT", m.Root); err != nil {
				return true, 1, err
			}
			return false, 0, nil
		}
	}
	s, err := m.Status()
	if errors.Is(err, os.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return true, 1, err
	}
	if s.Removing {
		return true, 1, errors.New("Freeway removal is scheduled; close the remaining Freeway processes")
	}
	root, err := m.root()
	if err != nil {
		return true, 1, err
	}
	target := filepath.Join(root, "versions", s.Active, exe("freeway"))
	if _, err = validatePackage(filepath.Dir(target), s.Active); err != nil {
		return true, 1, err
	}
	cmd := exec.Command(target, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "FREEWAY_INSTALL_ROOT="+root)
	if err = cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return true, exit.ExitCode(), nil
		}
		return true, 1, err
	}
	return true, 0, nil
}

// Uninstall removes only the managed program. Provider accounts and gateway data are preserved.
// Windows schedules a temporary helper because a running executable cannot delete itself.
func (m Manager) Uninstall(ctx context.Context) (bool, error) {
	root, err := m.root()
	if err != nil {
		return false, err
	}
	if runtime.GOOS != "windows" {
		return false, m.RemoveFiles(ctx)
	}
	unlock, err := lock(filepath.Join(root, ".update.lock"))
	if err != nil {
		return false, err
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	s, err := m.Status()
	if err != nil {
		return false, err
	}
	self, err := os.Executable()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return false, err
	}
	dir, err := os.MkdirTemp("", "freeway-uninstall-")
	if err != nil {
		return false, err
	}
	helper := filepath.Join(dir, "freeway.exe")
	if err = os.WriteFile(helper, data, 0700); err != nil {
		return false, err
	}
	log, err := os.OpenFile(filepath.Join(dir, "uninstall.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	defer log.Close()
	s.Removing = true
	encoded, _ := json.MarshalIndent(s, "", "  ")
	if err = atomicWrite(filepath.Join(root, "install.json"), encoded, 0600); err != nil {
		return false, err
	}
	unlock()
	unlock = nil
	cmd := exec.Command(helper, "__remove", "--root", root)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		return false, err
	}
	_ = cmd.Process.Release()
	return true, nil
}

// RemoveFiles is also used by the detached Windows helper after the application exits.
func (m Manager) RemoveFiles(ctx context.Context) error {
	root, err := m.root()
	if err != nil {
		return err
	}
	if _, err = m.Status(); err != nil {
		return err
	}
	release, err := lock(filepath.Join(root, ".update.lock"))
	if err != nil {
		return err
	}
	defer release()
	for attempt := 0; attempt < 100; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		binErr := os.Remove(filepath.Join(root, "bin", exe("freeway")))
		if errors.Is(binErr, os.ErrNotExist) {
			binErr = nil
		}
		versionErr := os.RemoveAll(filepath.Join(root, "versions"))
		if binErr == nil && versionErr == nil {
			removeSetupShortcut(root)
			return os.Remove(filepath.Join(root, "install.json"))
		}
		if runtime.GOOS != "windows" {
			return errors.Join(binErr, versionErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return errors.New("close all Freeway processes and retry removal; user data was preserved")
}

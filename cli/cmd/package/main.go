// Package main builds the native client distribution; it never publishes releases.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wooinwoo/freeway-cli/cli/update"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	rootArg := flag.String("root", "..", "repository root")
	outArg := flag.String("out", "", "output directory (default ROOT/dist/native)")
	goBin := flag.String("go", "go", "Go executable")
	targets := flag.String("targets", "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64", "comma-separated OS/architecture targets")
	baseURL := flag.String("base-url", "", "release asset HTTPS URL prefix")
	flag.Parse()
	root, err := filepath.Abs(*rootArg)
	if err != nil {
		return err
	}
	versionData, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return err
	}
	version := strings.TrimSpace(string(versionData))
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`).MatchString(version) {
		return fmt.Errorf("invalid VERSION")
	}
	out := *outArg
	if out == "" {
		out = filepath.Join(root, "dist", "native")
	}
	if err = os.MkdirAll(out, 0755); err != nil {
		return err
	}
	base := *baseURL
	if base == "" {
		base = "https://github.com/wooinwoo/freeway-cli/releases/download/v" + version
	}
	if !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("release asset base URL must use HTTPS")
	}
	common := map[string][]byte{}
	for _, name := range []string{"LICENSE", "THIRD_PARTY_NOTICES.md"} {
		common[name], err = os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
	}
	common["licenses/freeway-tui-LICENSE"], err = os.ReadFile(filepath.Join(root, "cli", "tui", "LICENSE"))
	if err != nil {
		return err
	}
	build, err := os.MkdirTemp("", "freeway-native-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(build)
	release := update.Release{SchemaVersion: 1, Version: version}
	var sums strings.Builder
	seen := map[string]bool{}
	for _, target := range strings.Split(*targets, ",") {
		parts := strings.Split(target, "/")
		if len(parts) != 2 || seen[target] {
			return fmt.Errorf("invalid or duplicate target: %s", target)
		}
		seen[target] = true
		goos, arch := parts[0], parts[1]
		if (goos != "linux" && goos != "windows" && goos != "darwin") || (arch != "amd64" && arch != "arm64") {
			return fmt.Errorf("unsupported target: %s", target)
		}
		if err = licenses(*goBin, root, common, goos, arch); err != nil {
			return err
		}
		files := map[string][]byte{}
		for name, data := range common {
			files[name] = data
		}
		for _, binary := range []struct{ module, path, name string }{{"cli", "./cmd/freeway", "freeway"}, {"backend", "./cmd/providerctl", "freeway-providers"}} {
			name := binary.name
			if goos == "windows" {
				name += ".exe"
			}
			output := filepath.Join(build, name)
			cmd := exec.Command(*goBin, "-C", filepath.Join(root, binary.module), "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.version="+version, "-o", output, binary.path)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+arch)
			cmd.Stderr = os.Stderr
			if err = cmd.Run(); err != nil {
				return fmt.Errorf("build %s %s: %w", target, name, err)
			}
			files[name], err = os.ReadFile(output)
			if err != nil {
				return err
			}
		}
		p := update.Package{SchemaVersion: 1, Version: version, OS: goos, Arch: arch, APIVersion: 1, Files: map[string]string{}}
		for name, data := range files {
			p.Files[name] = hash(data)
		}
		files["manifest.json"], err = json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		data, err := archive(files, goos == "windows")
		if err != nil {
			return err
		}
		name := "freeway_" + version + "_" + goos + "_" + arch + ".tar.gz"
		if goos == "windows" {
			name = strings.TrimSuffix(name, ".tar.gz") + ".zip"
		}
		if err = os.WriteFile(filepath.Join(out, name), data, 0644); err != nil {
			return err
		}
		sum := hash(data)
		fmt.Fprintf(&sums, "%s  %s\n", sum, name)
		release.Artifacts = append(release.Artifacts, update.Artifact{OS: goos, Arch: arch, URL: strings.TrimRight(base, "/") + "/" + name, SHA256: sum})
		fmt.Println(filepath.Join(out, name))
	}
	data, err := json.MarshalIndent(release, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(out, "release.json"), data, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0644)
}

func hash(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func licenses(goBin, root string, files map[string][]byte, goos, arch string) error {
	cmd := exec.Command(goBin, "env", "GOROOT")
	data, err := cmd.Output()
	if err != nil {
		return err
	}
	goRoot := strings.TrimSpace(string(data))
	files["licenses/go-LICENSE"], err = os.ReadFile(filepath.Join(goRoot, "LICENSE"))
	if err != nil {
		return err
	}
	type module struct {
		Path, Version, Dir string
		Main               bool
	}
	for _, entry := range []struct{ name, command string }{{"cli", "./cmd/freeway"}, {"backend", "./cmd/providerctl"}} {
		cmd = exec.Command(goBin, "-C", filepath.Join(root, entry.name), "list", "-deps", "-json", entry.command)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+arch)
		data, err = cmd.Output()
		if err != nil {
			return fmt.Errorf("list %s dependencies: %w", entry.name, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		seen := map[string]bool{}
		for {
			var dependency struct{ Module *module }
			err = decoder.Decode(&dependency)
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if dependency.Module == nil {
				continue
			}
			m := *dependency.Module
			if m.Main || seen[m.Path] {
				continue
			}
			seen[m.Path] = true
			prefix := "licenses/" + strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(m.Path) + "@" + m.Version + "/"
			found := false
			// Submodules can inherit their repository's license. Search only within
			// the module directory and its cache parents, never the working tree.
			dir := m.Dir
			for depth := 0; depth < 4 && dir != ""; depth++ {
				for _, entry := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "LICENCE", "COPYING", "NOTICE", "NOTICE.md"} {
					content, readErr := os.ReadFile(filepath.Join(dir, entry))
					if readErr == nil {
						files[prefix+entry] = content
						if strings.HasPrefix(entry, "LICENSE") || entry == "LICENCE" || entry == "COPYING" {
							found = true
						}
					}
				}
				if found {
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
			if !found {
				return fmt.Errorf("license missing for %s %s", m.Path, m.Version)
			}
		}
	}
	return nil
}

func archive(files map[string][]byte, windows bool) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	if windows {
		w := zip.NewWriter(&out)
		for _, name := range names {
			h := &zip.FileHeader{Name: name, Method: zip.Deflate}
			h.SetMode(0644)
			h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
			f, err := w.CreateHeader(h)
			if err != nil {
				return nil, err
			}
			if _, err = f.Write(files[name]); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
	} else {
		g := gzip.NewWriter(&out)
		w := tar.NewWriter(g)
		for _, name := range names {
			mode := int64(0644)
			if name == "freeway" || name == "freeway-providers" {
				mode = 0755
			}
			if err := w.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(files[name])), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}); err != nil {
				return nil, err
			}
			if _, err := w.Write(files[name]); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		if err := g.Close(); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

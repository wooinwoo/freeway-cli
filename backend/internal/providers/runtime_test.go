package providers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeWindowsNPMPackageCandidates(t *testing.T) {
	for _, id := range []string{"opencode", "cline"} {
		candidates := windowsNativeCandidates(t.TempDir(), id, "amd64")
		if len(candidates) < 3 {
			t.Fatal("native package candidates missing")
		}
		found := false
		for _, path := range candidates {
			if strings.Contains(path, "amd64") {
				t.Fatal("Go architecture leaked into npm package name")
			}
			if strings.Contains(path, "windows-x64") {
				found = true
			}
		}
		if !found {
			t.Fatalf("x64 native package missing for %s", id)
		}
	}
	if !strings.Contains(strings.Join(windowsNativeCandidates("prefix", "cline", "arm64"), " "), "windows-arm64") {
		t.Fatal("arm64 native package missing")
	}
}

func TestNativeCLIResolvesNPMOptionalBinaryWithoutLaunchingNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink layout; Windows package candidates have a separate fixture")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	modules := filepath.Join(root, "lib", "node_modules")
	wrapper := filepath.Join(modules, "opencode-ai", "bin", "opencode")
	packageName, _, err := installPackage("opencode", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(modules, packageName, "bin", "opencode")
	for _, dir := range []string{bin, filepath.Dir(wrapper), filepath.Dir(native)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(wrapper, []byte("#!/usr/bin/env node\nprocess.exit(88)\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("fixture native executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wrapper, filepath.Join(bin, "opencode")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_BIN", "")
	t.Setenv("FREEWAY_PROVIDER_HOME", filepath.Join(root, "uninstalled"))
	t.Setenv("PATH", bin)
	actual, err := CLIExecutable("opencode")
	if err != nil || actual != native {
		t.Fatalf("native discovery: %s %v", actual, err)
	}

	nested := filepath.Join(modules, "opencode-ai", "node_modules", packageName, "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(nested), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(native, nested); err != nil {
		t.Fatal(err)
	}
	actual, err = CLIExecutable("opencode")
	if err != nil || actual != nested {
		t.Fatalf("nested native discovery: %s %v", actual, err)
	}
	t.Setenv("OPENCODE_BIN", wrapper)
	if _, err := CLIExecutable("opencode"); err == nil {
		t.Fatal("explicit Node shim was accepted as a native CLI")
	}
	if client, err := New(Config{OpenCodeBinary: wrapper}); err == nil {
		client.Close()
		t.Fatal("explicit Config Node shim was accepted")
	}
	if client, err := New(Config{ClineBinary: wrapper}); err == nil {
		client.Close()
		t.Fatal("explicit Cline Config Node shim was accepted")
	}
	shell := filepath.Join(root, "shell-wrapper")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nexec node vendor.js\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if client, err := New(Config{OpenCodeBinary: shell}); err == nil {
		client.Close()
		t.Fatal("explicit shell wrapper was accepted as native")
	}
}

func TestNativeWindowsNPMShimResolvesAnExistingX64Binary(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("Windows x64 discovery fixture")
	}
	for _, id := range []string{"opencode", "cline"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("FREEWAY_PROVIDER_HOME", filepath.Join(root, "not-installed"))
			t.Setenv("PATH", root)
			t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
			t.Setenv(strings.ToUpper(id)+"_BIN", "")
			if err := os.WriteFile(filepath.Join(root, id+".cmd"), []byte("@node vendor-wrapper.js"), 0700); err != nil {
				t.Fatal(err)
			}
			candidates := windowsNativeCandidates(root, id, "amd64")
			native := ""
			for _, candidate := range candidates {
				if strings.Contains(candidate, "windows-x64") {
					native = candidate
					break
				}
			}
			if native == "" {
				t.Fatal("native optional package missing")
			}
			if err := os.MkdirAll(filepath.Dir(native), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(native, []byte("fixture native file"), 0700); err != nil {
				t.Fatal(err)
			}
			actual, err := CLIExecutable(id)
			if err != nil || actual != native {
				t.Fatalf("npm native discovery: %q %v", actual, err)
			}
		})
	}
}

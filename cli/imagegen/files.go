// SPDX-License-Identifier: MIT

package imagegen

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func imagePath(id, ext string) (string, error) {
	if !validToolName(id) || id == "." || id == ".." || ext != ".png" && ext != ".jpeg" {
		return "", errors.New("invalid image file identity")
	}
	dir, err := configDir()
	return filepath.Join(dir, "images", id+ext), err
}

func saveImage(ctx context.Context, id, ext string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := imagePath(id, ext)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func Find(id string) string {
	for _, ext := range []string{".png", ".jpeg"} {
		path, err := imagePath(id, ext)
		if err != nil {
			return ""
		}
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func Open(ctx context.Context, id string) error {
	path := Find(id)
	if path == "" {
		return errors.New("the generated image file was not found")
	}
	open := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = WithoutTokenEnv()
		return cmd.Run()
	}
	switch runtime.GOOS {
	case "darwin":
		return open("open", path)
	case "windows":
		return open("rundll32.exe", "url.dll,FileProtocolHandler", path)
	default:
		if bin, err := exec.LookPath("wslview"); err == nil && os.Getenv("WSL_DISTRO_NAME") != "" {
			return open(bin, path)
		}
		return open("xdg-open", path)
	}
}

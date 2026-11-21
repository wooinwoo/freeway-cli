//go:build !windows

package update

import "os/exec"

func hideVersionProbe(*exec.Cmd) {}

func removeSetupShortcut(string) {}

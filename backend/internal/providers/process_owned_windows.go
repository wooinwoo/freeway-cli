package providers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func configureOwnedProcess(cmd *exec.Cmd) (func(), error) {
	stop := func() error {
		if cmd.Process == nil {
			return nil
		}
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		err := exec.Command(filepath.Join(root, "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		if err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.Cancel = stop
	cmd.WaitDelay = 2 * time.Second
	return func() { _ = stop() }, nil
}

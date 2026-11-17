package providers

import (
	"os/exec"
	"strconv"
	"time"
)

func configureLoginCancellation(cmd *exec.Cmd) error {
	taskkill, err := exec.LookPath("taskkill.exe")
	if err != nil {
		return err
	}
	cmd.Cancel = func() error {
		return exec.Command(taskkill, "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
	}
	cmd.WaitDelay = time.Second
	return nil
}

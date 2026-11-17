//go:build !windows

package providers

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func configureLoginCancellation(cmd *exec.Cmd) error {
	processList, err := exec.LookPath("ps")
	if err != nil {
		return err
	}
	// Preserve the terminal's foreground process group. Only this login's descendants are stopped.
	cmd.Cancel = func() error {
		output, err := exec.Command(processList, "-A", "-o", "pid=", "-o", "ppid=").Output()
		if err != nil {
			_ = cmd.Process.Kill()
			return err
		}
		children := map[int][]int{}
		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) != 2 {
				continue
			}
			pid, pidErr := strconv.Atoi(fields[0])
			parent, parentErr := strconv.Atoi(fields[1])
			if pidErr == nil && parentErr == nil && pid > 0 {
				children[parent] = append(children[parent], pid)
			}
		}
		var stop func(int)
		stop = func(pid int) {
			for _, child := range children[pid] {
				stop(child)
				_ = syscall.Kill(child, syscall.SIGKILL)
			}
		}
		stop(cmd.Process.Pid)
		err = cmd.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return nil
}

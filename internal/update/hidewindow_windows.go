package update

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console child process from flashing a window.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

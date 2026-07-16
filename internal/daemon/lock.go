package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// AcquirePIDLock writes a PID file if no other live process holds it.
func AcquirePIDLock(path string) error {
	if b, err := os.ReadFile(path); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid > 0 && processAlive(pid) {
			return fmt.Errorf("meetingd already running (pid %d)", pid)
		}
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
}

// ReleasePIDLock removes the PID file if it belongs to this process.
func ReleasePIDLock(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid == os.Getpid() {
		_ = os.Remove(path)
	}
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks existence without killing (Unix).
	err = p.Signal(syscall.Signal(0))
	return err == nil
}

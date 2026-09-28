package git

import "golang.org/x/sys/unix"

// waitExited blocks until the process pid has exited, without reaping it:
// WNOWAIT leaves the zombie to Wait.
func waitExited(pid int) {
	var info unix.Siginfo
	for {
		if err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil); err != unix.EINTR {
			return
		}
	}
}

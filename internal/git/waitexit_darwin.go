package git

import "golang.org/x/sys/unix"

// waitExited blocks until the process pid has exited, without reaping it:
// kqueue's NOTE_EXIT reports the exit and leaves the zombie to Wait.
func waitExited(pid int) {
	kq, err := unix.Kqueue()
	if err != nil {
		return
	}
	defer func() { _ = unix.Close(kq) }()
	var ev unix.Kevent_t
	unix.SetKevent(&ev, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	ev.Fflags = unix.NOTE_EXIT
	out := make([]unix.Kevent_t, 1)
	for {
		// ESRCH: it has exited already, and is a zombie until reaped.
		if _, err := unix.Kevent(kq, []unix.Kevent_t{ev}, out, nil); err != unix.EINTR {
			return
		}
	}
}

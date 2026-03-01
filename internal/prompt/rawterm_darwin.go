//go:build darwin

package prompt

import (
	"fmt"
	"syscall"
	"unsafe"
)

func makeRaw(fd int) (func(), error) {
	var oldState syscall.Termios
	if _, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCGETA),
		uintptr(unsafe.Pointer(&oldState)),
		0, 0, 0,
	); errno != 0 {
		return nil, fmt.Errorf("set raw terminal: %w", errno)
	}

	newState := oldState
	newState.Lflag &^= syscall.ECHO | syscall.ICANON
	newState.Cc[syscall.VMIN] = 1
	newState.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCSETA),
		uintptr(unsafe.Pointer(&newState)),
		0, 0, 0,
	); errno != 0 {
		return nil, fmt.Errorf("set raw terminal: %w", errno)
	}

	restore := func() {
		_, _, _ = syscall.Syscall6(
			syscall.SYS_IOCTL,
			uintptr(fd),
			uintptr(syscall.TIOCSETA),
			uintptr(unsafe.Pointer(&oldState)),
			0, 0, 0,
		)
	}
	return restore, nil
}

//go:build !linux && !darwin

package prompt

import "fmt"

func makeRaw(fd int) (func(), error) {
	_ = fd
	return nil, fmt.Errorf("raw terminal mode not supported on this platform")
}

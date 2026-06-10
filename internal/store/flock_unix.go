//go:build unix

package store

import (
	"os"
	"syscall"
)

// lockSidecar serializes the short critical section of Save across processes.
func lockSidecar(path string) (func(), error) {
	lf, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		lf.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck
		lf.Close()
	}, nil
}

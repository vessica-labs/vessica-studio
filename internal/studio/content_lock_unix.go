//go:build darwin || linux

package studio

import (
	"os"
	"path/filepath"
	"syscall"
)

// LockContent coordinates engine writes with local projection replacement.
// The kernel releases the lock even if a process crashes.
func LockContent(root, name string) (func(), error) {
	if name != "content" && name != "cloud-sync" {
		return nil, os.ErrInvalid
	}
	rel := ".vstd/" + name + ".lock"
	if err := CheckContentPath(root, rel); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, ".vstd"), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, rel), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }, nil
}

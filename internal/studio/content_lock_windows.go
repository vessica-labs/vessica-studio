//go:build windows

package studio

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

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
	overlap := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlap); err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlap); _ = file.Close() }, nil
}

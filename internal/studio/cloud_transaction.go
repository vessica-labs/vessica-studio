package studio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const cloudTransactionPath = ".vstd/cloud-transaction.json"

var ErrCloudRecovery = errors.New("interrupted cloud pull; run vstd cloud workspace recover --root DIR before editing")

// A write-ahead journal makes replacement recoverable even when a process dies.
// It contains only the validated content projection, never credentials or Git state.
type cloudTransaction struct {
	Version  int
	Before   []ContentFile
	Incoming []string
}

func cloudRecoveryPending(root string) error {
	if err := CheckContentPath(root, cloudTransactionPath); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(root, cloudTransactionPath)); err == nil {
		return ErrCloudRecovery
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func beginCloudTransaction(root string, before, after []ContentFile) error {
	if err := cloudRecoveryPending(root); err != nil {
		return err
	}
	tx := cloudTransaction{Version: 1, Before: before}
	for _, f := range after {
		tx.Incoming = append(tx.Incoming, f.Path)
	}
	bytes, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	path := filepath.Join(root, cloudTransactionPath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := syncCloudDirectory(root); err != nil {
		return err
	}
	// Exclusive creation also prevents two concurrent pulls from sharing a journal.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(bytes); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return syncCloudDirectory(filepath.Dir(path))
}

func finishCloudTransaction(root string) error {
	path := filepath.Join(root, cloudTransactionPath)
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncCloudDirectory(filepath.Dir(path))
}

// RecoverCloudContent is an explicit local-only recovery operation. Stop other
// writers before invoking it; the user elects to restore the pre-pull projection.
func RecoverCloudContent(root string) error {
	if err := CheckContentPath(root, cloudTransactionPath); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(root, cloudTransactionPath))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 192<<20))
	if err != nil {
		return err
	}
	var tx cloudTransaction
	if json.Unmarshal(data, &tx) != nil || tx.Version != 1 {
		return fmt.Errorf("invalid cloud recovery journal; preserve it for manual recovery")
	}
	if len(tx.Before) > 0 {
		if err := ValidateCloudContent(tx.Before); err != nil {
			return err
		}
	}
	before := map[string]bool{}
	for _, file := range tx.Before {
		before[file.Path] = true
	}
	for _, path := range tx.Incoming {
		if !allowedCloudPath(path) {
			return fmt.Errorf("invalid cloud recovery path")
		}
		if err := CheckContentPath(root, path); err != nil {
			return err
		}
	}
	for _, file := range tx.Before {
		if err := CheckContentPath(root, file.Path); err != nil {
			return err
		}
	}
	for _, path := range tx.Incoming {
		if !before[path] {
			if err := os.Remove(filepath.Join(root, path)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	for _, file := range tx.Before {
		path := filepath.Join(root, file.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		mode := os.FileMode(file.Mode)
		if mode == 0 {
			mode = 0644
		}
		if err := writeCloudFile(path, file.Content, mode); err != nil {
			return err
		}
	}
	return finishCloudTransaction(root)
}

func syncCloudDirectory(path string) error {
	// Windows does not support fsync on directory handles; file flush and rename
	// still preserve the journal for process-interruption recovery.
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

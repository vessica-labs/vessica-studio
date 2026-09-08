package cloudauth

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

func acquireCredentialFileLock(ctx context.Context, path string) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("invalid credential lock")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		locked, err := tryCredentialLock(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		if locked {
			var once sync.Once
			return func() { once.Do(func() { releaseCredentialLock(file); file.Close() }) }, nil
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

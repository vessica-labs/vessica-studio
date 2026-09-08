//go:build !unix && !windows

package cloudauth

import (
	"errors"
	"os"
)

func tryCredentialLock(*os.File) (bool, error) {
	return false, errors.New("credential locking unavailable")
}
func releaseCredentialLock(*os.File) {}

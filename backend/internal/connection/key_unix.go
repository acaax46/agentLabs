//go:build !windows

package connection

import (
	"fmt"
	"os"
)

func protectKey(key []byte) ([]byte, error)   { return key, nil }
func unprotectKey(key []byte) ([]byte, error) { return key, nil }
func checkKeyPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("credential master key must be accessible only to its owner (0600)")
	}
	return nil
}

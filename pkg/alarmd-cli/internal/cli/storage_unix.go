//go:build darwin || linux

package cli

import "os"

func validateStoragePath(string) error { return nil }

func protectPath(path string, directory bool) error {
	if directory {
		return os.Chmod(path, 0700)
	}
	return os.Chmod(path, 0600)
}

func commitFile(source, destination string) error { return os.Rename(source, destination) }

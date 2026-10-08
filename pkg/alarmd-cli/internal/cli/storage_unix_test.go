//go:build darwin || linux

package cli

import (
	"os"
	"testing"
)

func assertPrivatePath(t *testing.T, path string, directory bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0600)
	if directory {
		want = 0700
	}
	if info.Mode().Perm() != want {
		t.Errorf("unsafe mode for %s: %v", path, info.Mode())
	}
}

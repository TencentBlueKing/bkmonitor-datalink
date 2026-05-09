package generate_support_files

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceLimitSetDoesNotTrimFollowingNewline(t *testing.T) {
	paths := []string{"write_root_conf_tpl.sh"}

	err := filepath.WalkDir("../../support-files/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Base(path) == "bkmonitorbeat.conf.tpl" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk support-files templates: %v", err)
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if strings.Contains(string(content), "{%- set resource_limit = resource_limit | default({}) -%}") {
			t.Fatalf("%s trims the newline after resource_limit set, which can render invalid YAML", path)
		}
	}
}

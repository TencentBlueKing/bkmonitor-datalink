// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procconf

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

func TestEnsureDirAccessibleFixesMissingOwnerExecute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are not available on Windows")
	}

	dir := filepath.Join(t.TempDir(), "bkmonitorbeat")
	if err := os.Mkdir(dir, 0o600); err != nil {
		t.Fatalf("create test dir: %v", err)
	}

	if err := ensureDirAccessible(dir); err != nil {
		t.Fatalf("ensure dir accessible: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat test dir: %v", err)
	}
	if got := info.Mode().Perm(); got&0o100 == 0 {
		t.Fatalf("owner execute bit not set, mode=%o", got)
	}
}

func TestEnsureDirAccessibleCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "bkmonitorbeat")

	if err := ensureDirAccessible(dir); err != nil {
		t.Fatalf("ensure dir accessible: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat test dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected a directory: %s", dir)
	}
}

func TestEnsureDirAccessibleFixesLegacyDirectoryAndWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are not available on Windows")
	}

	dir := filepath.Join(t.TempDir(), "bkmonitorbeat")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create test dir: %v", err)
	}
	if err := os.Chmod(dir, 0o144); err != nil {
		t.Fatalf("set legacy directory mode: %v", err)
	}

	if err := ensureDirAccessible(dir); err != nil {
		t.Fatalf("ensure dir accessible: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat test dir: %v", err)
	}
	if got := info.Mode().Perm(); got&0o700 != 0o700 {
		t.Fatalf("owner permissions not repaired, mode=%o", got)
	}

	dst := filepath.Join(dir, procConfName)
	if err := os.WriteFile(dst, []byte("test"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}
}

func TestNewWithExistingConfigDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are not available on Windows")
	}
	for _, tt := range []struct {
		name                     string
		mode, fileMode, wantMode os.FileMode
	}{
		{"legacy with writable config", 0o144, 0o600, 0o744},
		{"legacy with restrictive umask", 0o100, 0o600, 0o700},
		{"read-only directory and config", 0o555, 0o400, 0o555},
		{"read-only directory without config", 0o555, 0, 0o555},
		{"write and traverse only", 0o300, 0, 0o300},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.fileMode != 0 {
				if err := os.WriteFile(filepath.Join(dir, procConfName), []byte("config"), tt.fileMode); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(dir, tt.mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			New(nil, &configs.ProcConfig{DstDir: dir})
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tt.wantMode {
				t.Fatalf("mode = %o, want %o", got, tt.wantMode)
			}
			if tt.wantMode != tt.mode {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 || entries[0].Name() != procConfName {
					t.Fatalf("repaired directory entries = %v, err = %v", entries, err)
				}
			}
		})
	}
}

func TestEnsureDirAccessiblePreservesSpecialBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory special bits are not available on Windows")
	}

	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "setgid", mode: os.ModeSetgid | 0o600},
		{name: "sticky", mode: os.ModeSticky | 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bkmonitorbeat")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatalf("create test dir: %v", err)
			}
			if err := os.Chmod(dir, tt.mode); err != nil {
				t.Fatalf("set special directory mode: %v", err)
			}

			if err := ensureDirAccessible(dir); err != nil {
				t.Fatalf("ensure dir accessible: %v", err)
			}

			info, err := os.Stat(dir)
			if err != nil {
				t.Fatalf("stat test dir: %v", err)
			}
			if info.Mode()&tt.mode != tt.mode {
				t.Fatalf("special bits not preserved, mode=%v", info.Mode())
			}
			if got := info.Mode().Perm(); got&0o700 != 0o700 {
				t.Fatalf("owner permissions not repaired, mode=%o", got)
			}
		})
	}
}

func TestNewPreservesWritableConfigDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are not available on Windows")
	}

	dir := t.TempDir()
	dstfile := filepath.Join(dir, procConfName)
	if err := os.WriteFile(dstfile, []byte("test"), 0o600); err != nil {
		t.Fatalf("create target file: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("set directory mode: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
	})

	New(nil, &configs.ProcConfig{DstDir: dir})

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat test dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o555 {
		t.Fatalf("directory mode changed to %o, want 555", got)
	}
	if err := os.WriteFile(dstfile, []byte("updated"), 0o600); err != nil {
		t.Fatalf("overwrite target file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte("test"), 0o600); err == nil {
		t.Skip("current user can create files in a 0555 directory")
	} else if !os.IsPermission(err) {
		t.Fatalf("expected permission error, got: %v", err)
	}
}

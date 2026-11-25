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

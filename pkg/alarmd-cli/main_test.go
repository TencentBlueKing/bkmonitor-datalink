// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRealBinaryHelpAndVersionWithoutConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarmd-cli")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X main.version=test-build", "-o", path, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := filepath.Join(t.TempDir(), "does-not-exist")
	for _, arg := range []string{"--help", "--version"} {
		cmd := exec.Command(path, arg)
		cmd.Env = append(os.Environ(), "ALARMD_CLI_CONFIG_DIR="+root)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if arg == "--help" {
			for _, need := range []string{"auth login", "profile list", "discover --env", "describe <operation>", "invoke <operation>", "meta.result_file", "3=partial"} {
				if !strings.Contains(string(out), need) {
					t.Errorf("help missing %q", need)
				}
			}
		} else {
			var version map[string]any
			if err := json.Unmarshal(out, &version); err != nil || version["version"] != "test-build" {
				t.Fatalf("version: %s", out)
			}
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("help/version created configuration: %v", err)
	}
}

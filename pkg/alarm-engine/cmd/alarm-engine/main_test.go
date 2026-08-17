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
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPrintsVersionWithoutLoadingConfiguration(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--version", "--config", "/missing/config.yaml"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"alarm-engine", "version=", "commit=", "schema_version="} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("version output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestRunRejectsOwnerConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarm-engine.yaml")
	if err := os.WriteFile(path, []byte("mode: owner\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--config", path}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run() accepted owner configuration, stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "mode") {
		t.Fatalf("run() stderr = %q, want mode context", stderr.String())
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--unknown"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("run() accepted an unknown flag")
	}
	if stderr.Len() == 0 {
		t.Fatal("run() did not report flag error")
	}
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunGeneratesRequestedMix(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := run([]string{
		"-seed", "7",
		"-mix", "active=2,recovered=1",
		"-tenant-count", "2",
		"-min-updates", "1",
		"-max-updates", "1",
		"-duplicates", "1",
		"-invalid", "2",
	}, stdout, stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	// 2 active + (trigger/update/recover) + 1 duplicate + 2 invalid。
	if len(lines) != 8 {
		t.Fatalf("generated lines = %d, want 8", len(lines))
	}
	if !strings.Contains(stderr.String(), "scenarios=3") || !strings.Contains(stderr.String(), "records=8") {
		t.Fatalf("summary = %s", stderr)
	}
}

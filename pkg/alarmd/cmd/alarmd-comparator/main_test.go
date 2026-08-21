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
	"strings"
	"testing"
)

func TestComparatorVersionDoesNotOpenRuntime(t *testing.T) {
	t.Parallel()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-version"}, stdout, stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "alarmd-comparator") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

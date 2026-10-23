// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func preflightLine(t *testing.T, counts Counts, stage string) map[string]any {
	t.Helper()
	var output bytes.Buffer
	renderTo(&output, Observation{
		Component: ComponentState, Stage: Stage(stage), Result: ResultSuccess, Counts: counts,
	})
	line := strings.TrimSpace(output.String())
	if line == "" {
		t.Fatalf("nothing was logged for stage %s", stage)
	}
	fields := map[string]any{}
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	return fields
}

// The preflight's fetch and decode split is on its line at every value, zero
// included, and on no other line.
func TestThePreflightTimingIsOnItsLineAtZero(t *testing.T) {
	t.Parallel()
	fields := preflightLine(t, Counts{Keys: 256, StateBytes: 126762}, StageStatePreflight)
	for _, name := range []string{"fetch_ms", "decode_ms"} {
		value, present := fields[name]
		if !present {
			t.Errorf("%s is missing from a preflight line where it is zero", name)
			continue
		}
		if value != float64(0) {
			t.Errorf("%s = %v, want 0", name, value)
		}
	}
	if _, present := fields["events"]; present {
		t.Error("events is on the line at zero: only the timing split is exempt from the line's omission rule")
	}
	other := preflightLine(t, Counts{Keys: 4}, StageGapLoaded)
	for _, name := range []string{"fetch_ms", "decode_ms"} {
		if _, present := other[name]; present {
			t.Errorf("%s is on the %s line, which does not produce it", name, StageGapLoaded)
		}
	}
}

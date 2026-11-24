// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A point event's line has no duration_ms: a refused front read is a
// request refused, nothing timed, and its line used to read duration_ms 0.
// A timed one's has it, a measured zero included: a Slot timing whose
// clock did not move is a measurement of zero, marked so, not a point
// event.
func TestAPointEventsLineHasNoDurationAndAMeasuredZeroIsTimed(t *testing.T) {
	out := &lockedBuffer{}
	at := time.Unix(1_800_000_000, 0)
	frontRefusedLine(observability.New("alarmd", out), func() time.Time { return at })("operation_unknown", "strategy.get")
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.out.String())), &line); err != nil {
		t.Fatalf("line %q: %v", out.out.String(), err)
	}
	if line["stage"] != "front_read_refused" || line["result"] != "operation_unknown" {
		t.Fatalf("line %v, want the refused read", line)
	}
	if value, ok := line["duration_ms"]; ok {
		t.Fatalf("point event line carries duration_ms %v: %v", value, line)
	}

	var seen []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		seen = append(seen, observation)
	})
	startSlotTiming(context.Background(), observer, observability.StageSlotSourceCompleted, func() time.Time { return at })()
	if len(seen) != 1 || seen[0].Duration != 0 || !seen[0].Timed() {
		t.Fatalf("observations %+v: want one measured zero, timed", seen)
	}
}

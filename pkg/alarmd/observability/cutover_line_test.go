// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A cutover that kept every Query Group has nothing new on its line: it stays
// under the hourly sample - one line in the hour, marked as the sample -
// while a cutover that changed one is written every time (the controlplane
// tests read those from a publication). A publication always changes some
// Query Group, so the kept cutover is handed to the line here as the
// cutover would hand it.
func TestACutoverThatKeptEveryQueryGroupStaysUnderTheHourlySample(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 1024}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logging := NewLoggingObserver(New("alarmd", &output), policy)
	for round := 0; round < 3; round++ {
		logging.Observe(context.Background(), Observation{Component: ComponentControlPlane, Stage: StageScheduleCutover, Result: ResultSuccess,
			ScheduleCutover: &ScheduleCutoverFacts{Result: "success", QueryGroups: map[string]int{"kept": 2900}}})
	}
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %v: %q", err, raw)
		}
		lines = append(lines, line)
	}
	if len(lines) != 1 || lines[0]["sampled"] != true || lines[0]["cutover_kept"] != float64(2900) {
		t.Fatalf("lines %v, want the one sampled line of three kept cutovers", lines)
	}
}

// A cutover that failed wrote nothing, so what it had decided by then is no
// count of anything: its line carries the reason and the Query Group it
// stopped at, not the counts, and its failures keep their own limit even
// when it had decided changes before it stopped.
func TestAFailedCutoverIsLimitedAsAFailureAndCarriesNoCounts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 1024}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logging := NewLoggingObserver(New("alarmd", &output), policy)
	for round := 0; round < 2; round++ {
		logging.Observe(context.Background(), Observation{Component: ComponentControlPlane, Stage: StageScheduleCutover, Result: ResultFailed,
			ScheduleCutover: &ScheduleCutoverFacts{Result: "failure", Reason: "schedule_conflict", QueryGroup: "group-stopped",
				QueryGroups: map[string]int{"kept": 3, "revised": 1}, Samples: map[string][]string{"revised": {"group-revised"}}}})
	}
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %v: %q", err, raw)
		}
		lines = append(lines, line)
	}
	if len(lines) != 1 || lines[0]["cutover_query_group"] != "group-stopped" {
		t.Fatalf("lines %v, want one of the two failures, naming where it stopped", lines)
	}
	for _, field := range []string{"cutover_kept", "cutover_revised", "cutover_revised_sample"} {
		if _, present := lines[0][field]; present {
			t.Fatalf("a failed cutover's line carries %s: %v", field, lines[0])
		}
	}
}

// Whatever facts a cutover's line is handed, it names at most the cap of
// Query Groups for a decision, counts the rest as not named, and names them
// only for the decisions it samples: the line stays bounded.
func TestACutoversSamplesAreBoundedWhateverItIsHanded(t *testing.T) {
	named := make([]string, 20)
	for index := range named {
		named[index] = "group-" + strconv.Itoa(index)
	}
	normalized := NormalizeObservation(Observation{Stage: StageScheduleCutover, ScheduleCutover: &ScheduleCutoverFacts{Result: "success",
		Samples:          map[string][]string{"revised": named, "added": named[:18], "kept": {"group-kept"}},
		SamplesTruncated: map[string]int{"revised": 3, "added": -5, "cut": -1, "kept": 2}}}).ScheduleCutover
	if !reflect.DeepEqual(normalized.Samples, map[string][]string{"revised": named[:16], "added": named[:16]}) {
		t.Fatalf("samples %v, want the first 16 revised and added and no other decision", normalized.Samples)
	}
	if !reflect.DeepEqual(normalized.SamplesTruncated, map[string]int{"revised": 7, "added": 2}) {
		t.Fatalf("truncated %v, want 3 handed and 4 past the cap revised, a negative count as none and 2 past the cap added", normalized.SamplesTruncated)
	}
}

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
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func preflightLine(t *testing.T, counts Counts, stage string) map[string]any {
	t.Helper()
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 8})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
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

// The four counts of the second pass are on the preflight line at every value,
// zero included.
//
// Their zero is the answer: the migration is over when envelope_answered is
// zero and stays there, and the two corruption counts are read to confirm they
// are zero. Written only when non-zero, "none of these happened" and "nobody
// counted" are the same line -- which is what the count these replace did for
// a whole release, invisible on every line where it was zero, so that only a
// chance non-zero round revealed it was still alive.
func TestTheSecondPassCountsAreOnTheLineAtZero(t *testing.T) {
	t.Parallel()
	fields := preflightLine(t, Counts{Keys: 256, StateBytes: 126762}, StageStatePreflight)
	for _, name := range []string{"envelope_answered", "envelope_corrupt", "no_record_yet", "frame_corrupt_rescued", "frame_corrupt_lost"} {
		value, present := fields[name]
		if !present {
			t.Errorf("%s is missing from a preflight line where it is zero: a count that only appears when non-zero "+
				"cannot say that none happened", name)
			continue
		}
		if value != float64(0) {
			t.Errorf("%s = %v, want 0", name, value)
		}
	}
	// The counts that are not this indicator keep the line's existing rule:
	// a zero of theirs is not an answer anybody reads.
	if _, present := fields["events"]; present {
		t.Error("events is on the line at zero: only the second pass's counts are exempt from the line's omission rule")
	}
}

// And they are carried by the line that produces them, not by every line.
//
// Four keys on every observation in the process is most of a log line spent
// restating zeros nobody asked for; the preflight line is the only one that
// reads them.
func TestTheSecondPassCountsStayOffOtherLines(t *testing.T) {
	t.Parallel()
	fields := preflightLine(t, Counts{Keys: 4}, StageGapLoaded)
	for _, name := range []string{"envelope_answered", "envelope_corrupt", "no_record_yet", "frame_corrupt_rescued", "frame_corrupt_lost"} {
		if _, present := fields[name]; present {
			t.Errorf("%s is on the %s line, which does not produce it", name, StageGapLoaded)
		}
	}
}

// Where the preflight's time went is on its line at every value, zero
// included, and only on its line.
func TestThePreflightLineCarriesFetchAndDecodeTime(t *testing.T) {
	t.Parallel()
	fields := preflightLine(t, Counts{Keys: 250, StateFetchMillis: 30}, StageStatePreflight)
	if fields["fetch_ms"] != float64(30) {
		t.Errorf("fetch_ms = %v, want 30", fields["fetch_ms"])
	}
	if value, present := fields["decode_ms"]; !present || value != float64(0) {
		t.Errorf("decode_ms = %v present %v, want a zero that is on the line", value, present)
	}
	other := preflightLine(t, Counts{Keys: 4, StateFetchMillis: 30}, StageGapLoaded)
	for _, name := range []string{"fetch_ms", "decode_ms"} {
		if _, present := other[name]; present {
			t.Errorf("%s is on the %s line, which does not produce it", name, StageGapLoaded)
		}
	}
}

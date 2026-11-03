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
	"fmt"
	"strings"
	"testing"
	"time"
)

const inactive ReasonCode = "EFFECTIVE_TIME_INACTIVE"

// transitionLine is what a test reads of one written line.
type transitionLine struct {
	Group    string       `json:"query_group_key"`
	Slot     int64        `json:"evaluation_time"`
	Result   string       `json:"result"`
	Reason   string       `json:"reason_code"`
	Changed  bool         `json:"reason_changed"`
	Previous []ReasonCode `json:"previous_reasons"`
	Sampled  bool         `json:"sampled"`
}

// transitionObserver is the production policy over one hour of a fixed
// clock, so every hourly sample a test takes is still in its window.
func transitionObserver(t *testing.T, output *bytes.Buffer) *LoggingObserver {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 1024}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	return NewLoggingObserver(New("alarmd", output), policy)
}

func evaluated(group string, slot int64, series string, operation Operation, reason ReasonCode) Observation {
	result := Result(ResultSuccess)
	if reason != ReasonNone {
		result = ResultDegraded
	}
	return Observation{Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: result, ReasonCode: reason,
		Operation: operation, Trace: TraceFields{QueryGroupKey: group, EvaluationTime: slot, DimensionIdentityDigest: series}}
}

func writtenLines(t *testing.T, output *bytes.Buffer) []transitionLine {
	t.Helper()
	var lines []transitionLine
	for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if raw == "" {
			continue
		}
		var line transitionLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	output.Reset()
	return lines
}

// A Query Group out of its hours for two Slots and in them on its third
// writes the evaluation its reason changed on: the third Slot's first, a
// success that is otherwise never written, with the reason the Slot before
// ended with. The Slot's other series and the Slot after, the same again,
// write nothing. Back out of its hours on its fifth, the first INACTIVE
// evaluation is written although that reason's sample was taken this hour,
// and the Slot's next is not. Its first Slot wrote no change, having none
// to change from.
func TestTheEvaluationAGroupsReasonChangedOnIsWrittenWhateverItsResult(t *testing.T) {
	var output bytes.Buffer
	observer := transitionObserver(t, &output)
	ctx := context.Background()
	slot := func(at int64, reason ReasonCode) []transitionLine {
		for _, series := range []string{"s1", "s2"} {
			observer.Observe(ctx, evaluated("qg-a", at, series, OperationNormal, reason))
		}
		return writtenLines(t, &output)
	}
	if lines := slot(60, inactive); len(lines) != 1 || !lines[0].Sampled || lines[0].Changed {
		t.Fatalf("first Slot out of hours wrote %+v, want its hourly sample alone, no change", lines)
	}
	if lines := slot(120, inactive); len(lines) != 0 {
		t.Fatalf("second Slot out of hours wrote %+v, want nothing: the hour's sample is taken", lines)
	}
	lines := slot(180, ReasonNone)
	if len(lines) != 1 || !lines[0].Changed || lines[0].Slot != 180 || lines[0].Result != string(ResultSuccess) ||
		fmt.Sprint(lines[0].Previous) != fmt.Sprint([]ReasonCode{inactive}) {
		t.Fatalf("the first Slot in its hours wrote %+v, want one line, its first success, changed from %s", lines, inactive)
	}
	if lines := slot(240, ReasonNone); len(lines) != 0 {
		t.Fatalf("a second Slot in its hours wrote %+v, want nothing", lines)
	}
	lines = slot(300, inactive)
	if len(lines) != 1 || !lines[0].Changed || lines[0].Slot != 300 || lines[0].Reason != string(inactive) ||
		fmt.Sprint(lines[0].Previous) != fmt.Sprint([]ReasonCode{ReasonNone}) {
		t.Fatalf("back out of its hours wrote %+v, want its first INACTIVE line, changed from %s, its sample taken or not", lines, ReasonNone)
	}
	// Two reasons new to a Slot are one change: the first is written as it,
	// the second only as its own reason's sample.
	observer.Observe(ctx, evaluated("qg-a", 360, "s1", OperationNormal, "HISTORY_WARMING"))
	observer.Observe(ctx, evaluated("qg-a", 360, "s2", OperationNormal, "QUERY_TARGET_MISSING"))
	lines = writtenLines(t, &output)
	if len(lines) != 2 || !lines[0].Changed || lines[0].Reason != "HISTORY_WARMING" || lines[1].Changed || !lines[1].Sampled {
		t.Fatalf("a Slot with two new reasons wrote %+v, want its first as the change and its second as a sample", lines)
	}
}

// What is not a step of a group's own Slots changes nothing: a supplement's
// evaluation, a normal evaluation of a Slot older than the newest, another
// group's first Slot, each with a reason the group's newest Slot did not
// have.
// The group's next Slot still writes its change, from the reasons of the
// Slot the others did not touch.
func TestOnlyAGroupsOwnNextSlotIsItsReasonChange(t *testing.T) {
	var output bytes.Buffer
	observer := transitionObserver(t, &output)
	ctx := context.Background()
	for _, at := range []int64{60, 120} {
		observer.Observe(ctx, evaluated("qg-a", at, "s1", OperationNormal, ReasonNone))
	}
	writtenLines(t, &output)
	for _, other := range []Observation{
		evaluated("qg-a", 120, "s1", OperationSupplement, inactive),
		evaluated("qg-a", 60, "s1", OperationNormal, inactive),
		evaluated("qg-b", 120, "s1", OperationNormal, inactive),
	} {
		observer.Observe(ctx, other)
		for _, line := range writtenLines(t, &output) {
			if line.Changed {
				t.Fatalf("%s %s at %d wrote a change: %+v", other.Trace.QueryGroupKey, other.Operation, other.Trace.EvaluationTime, line)
			}
		}
	}
	observer.Observe(ctx, evaluated("qg-a", 180, "s1", OperationNormal, inactive))
	lines := writtenLines(t, &output)
	if len(lines) != 1 || !lines[0].Changed || fmt.Sprint(lines[0].Previous) != fmt.Sprint([]ReasonCode{ReasonNone}) {
		t.Fatalf("the group's next Slot wrote %+v, want its change from %s alone", lines, ReasonNone)
	}
}

// The groups remembered are bounded: past the bound the least recently
// evaluated is forgotten, and its next Slot is a first one again, with no
// change to write. Kept, the same Slot is its change.
func TestReasonTransitionsForgetTheLeastRecentlyEvaluatedGroupPastTheirBound(t *testing.T) {
	for _, c := range []struct {
		max     int
		changed bool
	}{{2, false}, {3, true}} {
		transitions := newReasonTransitions(c.max)
		for _, group := range []string{"a", "b", "c"} {
			transitions.observe(group, 60, ReasonNone)
		}
		if changed, _ := transitions.observe("a", 120, inactive); changed != c.changed || len(transitions.groups) > c.max || transitions.order.Len() > c.max {
			t.Fatalf("bound %d: group a's next Slot changed %v with %d groups held, want %v and no more than the bound",
				c.max, changed, len(transitions.groups), c.changed)
		}
	}
}

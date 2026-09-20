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
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestPhaseTwoRuntimeObserverKeepsOneLinePerReasonAndQueryGroup(t *testing.T) {
	t.Parallel()

	if _, err := newPhaseTwoRuntimeObserver(nil, observability.Discard("alarmd")); err == nil {
		t.Fatal("nil recorder was accepted")
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var output bytes.Buffer
	observer, err := newPhaseTwoRuntimeObserver(recorder, observability.New("alarmd", &output))
	if err != nil {
		t.Fatal(err)
	}
	failed := func(queryGroup string) observability.Observation {
		return observability.Observation{
			Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
			Result: observability.Result(observability.ResultFailed), Direction: observability.DirectionInternal,
			ReasonCode: observability.ReasonInternalUnknown, Err: errors.New("alarmd worker: duplicate completion binding"),
			QueryFailure: &observability.QueryFailureFacts{Stage: "stream_complete", Category: "completion_contract", Code: "DUPLICATE_COMPLETION_BINDING"},
		}
	}
	ctxA := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "query-group-a"})
	ctxB := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "query-group-b"})
	// Spend the whole of group A's budget and one more, so A is being
	// suppressed when B speaks.
	//
	// Written against the budget rather than against the number 1. What this
	// check is for is the scoping -- one noisy Query Group must not hide every
	// other one, which is the whole difference from the phase-one limiter below
	// -- and pinning "two lines" demonstrated that only while the budget
	// happened to be one line per window. Opening the budget up for the
	// development phase made this fail without anything about the scoping
	// changing.
	for round := 0; round <= phaseTwoDiagnosticLogMaxEvents; round++ {
		observer.Observe(ctxA, failed("query-group-a"))
	}
	observer.Observe(ctxB, failed("query-group-b"))

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != phaseTwoDiagnosticLogMaxEvents+1 {
		t.Fatalf("lines=%d, want %d: A's budget spent, the extra A suppressed, and B still admitted",
			len(lines), phaseTwoDiagnosticLogMaxEvents+1)
	}
	// The last line has to be B's. If the two shared a bucket, B would have been
	// suppressed behind A and the run would end on an A line.
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last["query_group_key"] != "query-group-b" {
		t.Fatalf("last line is %v, want query-group-b: a noisy Query Group is hiding another's diagnostics", last)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["query_group_key"] != "query-group-a" || first["failure_code"] != "DUPLICATE_COMPLETION_BINDING" ||
		first["error"] != "alarmd worker: duplicate completion binding" {
		t.Fatalf("line 0=%v, want coordinates, failure code and error text for query-group-a", first)
	}
	// This assertion used to end by running the phase-one observer on the same
	// two Query Groups and showing it emitted one line where this one emits
	// two. That contrast went when the phase-one runtime was retired, so the
	// property it demonstrated is stated here instead rather than left to a
	// comparison that no longer has a second side: the budget is per Query
	// Group, not per reason code, and a Query Group spending its own budget
	// must not consume anyone else's.
}

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
	observer.Observe(ctxA, failed("query-group-a"))
	observer.Observe(ctxA, failed("query-group-a"))
	observer.Observe(ctxB, failed("query-group-b"))
	observer.Observe(ctxA, failed("query-group-a"))

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d, want one line per (reason, query group) within the window: %s", len(lines), output.String())
	}
	for index, want := range []string{"query-group-a", "query-group-b"} {
		var event map[string]any
		if err := json.Unmarshal([]byte(lines[index]), &event); err != nil {
			t.Fatal(err)
		}
		if event["query_group_key"] != want || event["failure_code"] != "DUPLICATE_COMPLETION_BINDING" ||
			event["error"] != "alarmd worker: duplicate completion binding" {
			t.Fatalf("line %d=%v, want coordinates, failure code and error text for %s", index, event, want)
		}
	}
	// The phase-one constructor keeps its per-reason budget: the second Query
	// Group is hidden behind the first within the same window.
	var phaseOne bytes.Buffer
	legacy, err := newPhaseOneRuntimeObserver(metric.NewRecorder(metric.BuildInfo{}), observability.New("alarmd", &phaseOne))
	if err != nil {
		t.Fatal(err)
	}
	legacy.Observe(ctxA, failed("query-group-a"))
	legacy.Observe(ctxB, failed("query-group-b"))
	if got := len(strings.Split(strings.TrimSpace(phaseOne.String()), "\n")); got != 1 {
		t.Fatalf("phase-one behaviour changed: lines=%d", got)
	}
}

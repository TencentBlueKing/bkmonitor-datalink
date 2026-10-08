// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package simulation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/store/storetest"
)

var start = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

type targets struct{ err error }

func (t targets) ResolveScope(_ context.Context, tenant, space string) (onemodel.TargetScope, error) {
	return onemodel.TargetScope{TenantID: tenant, SpaceCode: space, BusinessIDs: []int64{2}}, t.err
}

func (t targets) Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	return onemodel.TargetResult{}, t.err
}

func event(id, source, fp, title string, offset int, action domain.EventAction) *domain.Event {
	e := storetest.Event("tenant", id, fp, "warning")
	e.EventSourceID = source
	e.Title = title
	e.Labels["source_id"] = domain.NewStringScalar("source")
	e.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
	at := start.Add(time.Duration(offset) * time.Second)
	e.OccurredAt = at
	e.ProducedAt = at
	e.ReceivedAt = at
	e.CreateAt = at
	e.Evaluations[0].Action = action
	e.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusSucceeded, EnrichedAt: &at, EnrichConfigDigest: "simulation-fixture", Enrich: domain.EventEnrichData{Evaluations: []domain.EvaluationEnrich{{Severity: "warning", Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"processors": json.RawMessage(`[]`)}}}}}
	return &e
}

func clipSpec(scheme string) json.RawMessage {
	return json.RawMessage(`{"name":"clip","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"scheme":` + scheme + `}`)
}

func request(kind policy.Kind, spec json.RawMessage, events ...*domain.Event) Request {
	q := Request{Scope: policy.Scope{TenantID: "tenant", Kind: kind}, Spec: spec}
	for _, e := range events {
		q.Steps = append(q.Steps, Step{At: e.ReceivedAt, Event: e})
	}
	return q
}

func run(t *testing.T, q Request) Response {
	t.Helper()
	r, err := New(nil, nil, targets{}, nil, "update_current").Run(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSimulationClipReplayActiveBypassAndTerminalCleanup(t *testing.T) {
	q := request(policy.Suppression, clipSpec(`[{"type":"clip","count":3,"duration":60,"duration_type":"second"}]`), event("a", "source-a", "fp", "db", 0, domain.EventActionTriggered), event("b", "source-a", "fp", "db", 30, domain.EventActionTriggered), event("c", "source-a", "fp", "db", 60, domain.EventActionTriggered), event("d", "source-a", "fp", "changed", 61, domain.EventActionTriggered), event("end", "source-a", "fp", "db", 62, domain.EventActionResolved), event("again", "source-a", "fp", "db", 63, domain.EventActionTriggered))
	q.Steps = append(q.Steps[:1], append([]Step{{At: start.Add(time.Second), Event: q.Steps[0].Event}}, q.Steps[1:]...)...)
	r := run(t, q)
	for _, i := range []int{0, 2, 6} {
		if r.Steps[i].Outcome != "alert_suppressed" {
			t.Fatalf("step %d: %+v", i, r.Steps[i])
		}
	}
	if !r.Steps[1].Replayed || r.Steps[3].Outcome != "alert_created" || r.Steps[4].Alerts[0].Title != "db" || r.Steps[5].Outcome != "alert_recovered" {
		t.Fatalf("incorrect lifecycle %+v", r.Steps)
	}
	if r.Steps[3].Decision.Suppression.Evaluations[0].Steps[0].Count != 3 || r.Steps[6].Decision.Suppression.Evaluations[0].Steps[0].Count != 1 {
		t.Fatal("closed interval or terminal cleanup mismatch")
	}
	again := run(t, q)
	if again.Steps[0].Outcome != "alert_suppressed" {
		t.Fatal("state leaked")
	}
}

func TestSimulationAggregationCrossSourceAndClosedDeadline(t *testing.T) {
	q := request(policy.Suppression, clipSpec(`[{"type":"aggregation","duration":60,"duration_type":"second","fields":["name"]}]`), event("a", "source-a", "one", "db", 0, domain.EventActionTriggered), event("b", "source-b", "two", "db", 60, domain.EventActionTriggered), event("c", "source-c", "three", "db", 61, domain.EventActionTriggered))
	r := run(t, q)
	if r.Steps[0].Outcome != "alert_created" || r.Steps[1].Outcome != "alert_suppressed" || r.Steps[2].Outcome != "alert_created" {
		t.Fatalf("aggregation boundary %+v", r.Steps)
	}
}

func mergeSpec(cyclic bool) json.RawMessage {
	flag := "false"
	if cyclic {
		flag = "true"
	}
	return json.RawMessage(`{"name":"merge","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":[{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"db"}},{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"app"}}],"merge_cycle":60,"is_cycle_merge":` + flag + `,"aggregate_fields":[],"new_alarm_config":[{"key":"name","value":"joint ${alarm_num}"},{"key":"level","value":"warning"}]}`)
}

func TestSimulationMergeModesAndTimeOnlyProgress(t *testing.T) {
	for _, cyclic := range []bool{false, true} {
		t.Run(map[bool]string{false: "early", true: "cyclic"}[cyclic], func(t *testing.T) {
			q := request(policy.Merge, mergeSpec(cyclic), event("a", "a", "one", "db", 0, domain.EventActionTriggered), event("b", "b", "two", "app", 1, domain.EventActionTriggered))
			q.Steps = append(q.Steps, Step{At: start.Add(time.Minute)})
			r := run(t, q)
			index := 1
			if cyclic {
				index = 2
				if len(r.Steps[1].Windows) != 0 {
					t.Fatal("cyclic judged early")
				}
			}
			if len(r.Steps[index].Windows) != 1 || r.Steps[index].Windows[0].Outcome != "succeeded" || len(r.Steps[index].Windows[0].Members) != 2 {
				t.Fatalf("bad merge %+v", r.Steps)
			}
		})
	}
	q := request(policy.Merge, mergeSpec(false), event("a", "a", "one", "db", 0, domain.EventActionTriggered))
	q.Steps = append(q.Steps, Step{At: start.Add(time.Minute)})
	r := run(t, q)
	if r.Steps[1].Windows[0].Outcome != "failed" || r.Steps[1].Alerts[0].Merge.Blocking() {
		t.Fatal("failed window retained")
	}
}

func TestSimulationRejectsInvalidScopeTimeBudgetAndCancellation(t *testing.T) {
	base := request(policy.Suppression, clipSpec(`[{"type":"clip","count":3,"duration":60,"duration_type":"second"}]`), event("a", "a", "f", "db", 0, domain.EventActionTriggered))
	for _, mutate := range []func(*Request){func(q *Request) { q.TenantID = "foreign" }, func(q *Request) { q.Steps[0].Event.EnrichStatus = domain.EnrichStatusPending }, func(q *Request) { q.Steps = append(q.Steps, Step{At: start.Add(-time.Second)}) }, func(q *Request) { q.Steps = append(q.Steps, Step{At: start.Add(31 * 24 * time.Hour)}) }, func(q *Request) { q.Steps = make([]Step, 129) }, func(q *Request) { q.Steps[0].EventID = "also" }, func(q *Request) { q.Kind = policy.Shield }, func(q *Request) { q.Spec = json.RawMessage(strings.Repeat(" ", 3<<20)) }} {
		raw, _ := json.Marshal(base)
		var q Request
		if err := json.Unmarshal(raw, &q); err != nil {
			t.Fatal(err)
		}
		mutate(&q)
		if _, err := New(nil, nil, targets{}, nil, "").Run(t.Context(), q); err == nil {
			t.Fatal("invalid accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := New(nil, nil, targets{}, nil, "").Run(ctx, base); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	s := New(nil, nil, targets{}, nil, "")
	s.slots <- struct{}{}
	s.slots <- struct{}{}
	if _, err := s.Run(t.Context(), base); !errors.Is(err, policy.ErrPreviewCapacity) {
		t.Fatal("capacity ignored")
	}
}

func TestSimulationConcurrentIsolationAndDependencySkip(t *testing.T) {
	q := request(policy.Suppression, clipSpec(`[{"type":"clip","count":3,"duration":60,"duration_type":"second"}]`), event("a", "a", "f", "db", 0, domain.EventActionTriggered))
	s := New(nil, nil, targets{err: policy.ErrUnavailable}, nil, "")
	r, err := s.Run(t.Context(), q)
	if err != nil || r.Steps[0].Outcome != "alert_created" || r.Steps[0].Decision.Suppression.Evaluations[0].Steps[0].Outcome != "skipped" {
		t.Fatalf("dependency %+v %v", r, err)
	}
	s = New(nil, nil, targets{}, nil, "")
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			r, err := s.Run(t.Context(), q)
			if err != nil || r.Steps[0].Outcome != "alert_suppressed" {
				t.Errorf("isolation %+v %v", r, err)
			}
		})
	}
	wg.Wait()
}

func TestSimulationExistingAlertUpgradeBypassesClip(t *testing.T) {
	q := request(policy.Suppression, clipSpec(`[{"type":"clip","count":2,"duration":60,"duration_type":"second"}]`), event("a", "source-a", "fp", "opening", 0, domain.EventActionTriggered), event("b", "source-a", "fp", "opening", 1, domain.EventActionTriggered), event("up", "source-a", "fp", "later", 2, domain.EventActionTriggered))
	up := q.Steps[2].Event
	up.Evaluations[0].Severity = "critical"
	up.Enrich.Evaluations[0].Severity = "critical"
	r := run(t, q)
	if r.Steps[2].Decision.Suppression.BypassReason != "active_alert" || len(r.Steps[2].Alerts) != 1 || r.Steps[2].Alerts[0].Severity != "critical" || r.Steps[2].Alerts[0].Title != "opening" {
		t.Fatal("active upgrade changed suppression/opening semantics")
	}
}

func TestSimulationClipSeparatesSourcesAndMergeRechecksTerminalMembers(t *testing.T) {
	q := request(policy.Suppression, clipSpec(`[{"type":"clip","count":2,"duration":60,"duration_type":"second"}]`), event("a", "a", "same", "db", 0, domain.EventActionTriggered), event("b", "b", "same", "db", 1, domain.EventActionTriggered))
	r := run(t, q)
	if r.Steps[0].Decision.Suppression.Evaluations[0].Steps[0].Count != 1 || r.Steps[1].Decision.Suppression.Evaluations[0].Steps[0].Count != 1 {
		t.Fatal("clip crossed sources")
	}
	q = request(policy.Merge, mergeSpec(true), event("a", "a", "db", "db", 0, domain.EventActionTriggered), event("b", "b", "app", "app", 1, domain.EventActionTriggered), event("end", "b", "app", "app", 2, domain.EventActionResolved))
	q.Steps = append(q.Steps, Step{At: start.Add(time.Minute)})
	r = run(t, q)
	if r.Steps[3].Windows[0].Outcome != "failed" || len(r.Steps[3].Windows[0].Members) != 1 {
		t.Fatal("ended member counted toward merge")
	}
	spec := json.RawMessage(strings.ReplaceAll(string(mergeSpec(false)), `"target_value":"app"`, `"target_value":"db"`))
	q = request(policy.Merge, spec, event("one", "a", "one", "db", 0, domain.EventActionTriggered))
	q.Steps = append(q.Steps, Step{At: start.Add(time.Minute)})
	r = run(t, q)
	if len(r.Steps[0].Windows) != 0 || r.Steps[1].Windows[0].Outcome != "failed" {
		t.Fatal("one Alert filled two groups and merged itself")
	}
}

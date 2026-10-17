// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type snapshotRecords struct{ rows []policy.Record }

func (r snapshotRecords) List(_ context.Context, scope policy.Scope, after string, limit int) ([]policy.Record, error) {
	rows := []policy.Record{}
	for _, row := range r.rows {
		if row.Scope == scope && row.ID > after {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows[:min(limit, len(rows))], nil
}

func snapshotRecord(t *testing.T, id, field string, value any, mappings map[string]policy.FieldMapping) policy.Record {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name": id, "is_enable": true, "updated_at": "2026-09-30T00:00:00Z", "space_code": "bkcc__2",
		"activate_times": []map[string]string{{"period": "everyday", "open_clock_time": "00:00:00", "close_clock_time": "23:59:59"}},
		"policy":         map[string]any{"expression": "A", "A": map[string]any{"condition": "term", "target_key": field, "target_value": value}},
		"scheme":         []map[string]any{{"type": "clip", "count": 2, "duration": 60, "duration_type": "second"}}, "field_mappings": mappings,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Suppression, raw)
	if err != nil {
		t.Fatal(err)
	}
	return policy.Record{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Suppression}, ID: id, Revision: 1, Published: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
}

func TestSnapshotCandidatesUnionAllEvaluationsAndMappings(t *testing.T) {
	event := storetest.Event("tenant", "event", "fp", "critical")
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered})
	event.Labels["owner"] = domain.NewStringScalar("alice")
	event.ExtraData["owner"] = json.RawMessage(`"bob"`)
	var err error
	event, err = event.WithEnrichment(storetest.Enrichment(event))
	if err != nil {
		t.Fatal(err)
	}
	rows := []policy.Record{
		snapshotRecord(t, "critical", "level", "fatal", nil), snapshotRecord(t, "warning", "level", "warning", nil), snapshotRecord(t, "info", "level", "remind", nil),
		snapshotRecord(t, "labels-owner", "owner", "alice", map[string]policy.FieldMapping{"owner": {Path: "$.labels.owner", Kind: policy.FieldKeyword}}),
		snapshotRecord(t, "extra-owner", "owner", "alice", map[string]policy.FieldMapping{"owner": {Path: "$.extra_data.owner", Kind: policy.FieldKeyword}}),
	}
	snapshot, err := (Snapshotter{Catalog: policy.NewCatalog(snapshotRecords{rows})}).Snapshot(t.Context(), event, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, ref := range snapshot.Releases {
		ids = append(ids, ref.ID)
	}
	if !slices.Equal(ids, []string{"critical", "labels-owner", "warning"}) {
		t.Fatal("candidate levels or mapping lost", ids)
	}
}

func TestEventCandidateReaderUsesConfiguredLevelAndEffectiveEnrich(t *testing.T) {
	event := storetest.Event("tenant", "event", "fp", "urgent")
	event.Labels["owner"] = domain.NewStringScalar("old")
	at := event.CreateAt
	event.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusSucceeded, EnrichedAt: &at, EnrichConfigDigest: "config", Enrich: domain.EventEnrichData{Evaluations: []domain.EvaluationEnrich{{Severity: "urgent", Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.owner","value":"new"}]}}]`)}}}}}
	reader := eventCandidateReader(event, func(severity string) (string, error) {
		if severity != "urgent" {
			return "", fmt.Errorf("unexpected severity")
		}
		return "fatal", nil
	})
	for _, test := range []struct {
		field   string
		mapping policy.FieldMapping
		want    string
	}{{"level", policy.FieldMapping{Kind: policy.FieldKeyword}, "fatal"}, {"owner", policy.FieldMapping{Kind: policy.FieldKeyword, Path: "$.labels.owner"}, "new"}} {
		got, err := reader(t.Context(), test.field, test.mapping)
		if err != nil || !got.Present || !slices.Equal(got.Data.([]any), []any{test.want}) {
			t.Fatal(test.field, got, err)
		}
	}
}

func TestSnapshotUnknownEnrichKeepsPossibleRules(t *testing.T) {
	event := storetest.Event("tenant", "event", "fp", "warning")
	// 没有已提交的丰富视图时无法证明字段未命中，候选仍须交正式匹配器诊断。
	row := snapshotRecord(t, "source", "source_id", "other", nil)
	snapshot, err := (Snapshotter{Catalog: policy.NewCatalog(snapshotRecords{[]policy.Record{row}})}).Snapshot(t.Context(), event, time.Now())
	if err != nil || len(snapshot.Releases) != 1 {
		t.Fatal(snapshot, err)
	}
}

type snapshotEnricherFunc func(context.Context, enrich.Input) (enrich.Result, error)

func (f snapshotEnricherFunc) Enrich(ctx context.Context, input enrich.Input) (enrich.Result, error) {
	return f(ctx, input)
}

func TestSnapshotFrozenSeveritySurvivesEnrichConfigChange(t *testing.T) {
	severity := runtimeconfig.NewSeverity(config.DefaultSeverityConfig())
	row := snapshotRecord(t, "legacy-critical", "level", "fatal", nil)
	catalog := policy.NewCatalog(snapshotRecords{[]policy.Record{row}})
	snapshotter := Snapshotter{Catalog: catalog, Level: func(name string) (string, error) {
		return severity.SeveritySnapshot().KACLevel(name)
	}}
	frozen := severity.SeveritySnapshot()
	scoped := snapshotter.WithLevelMapper(frozen.KACLevel)
	enricher := snapshotEnricherFunc(func(ctx context.Context, input enrich.Input) (enrich.Result, error) {
		next := severity.SeveritySnapshot()
		next.NativeNames, next.Digest = true, ""
		if err := severity.Install(next); err != nil {
			return enrich.Result{}, err
		}
		return (enrich.NoopEnricher{}).Enrich(ctx, input)
	})
	event := storetest.Event("tenant", "severity-change", "fp", "critical")
	event.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
	at := event.CreateAt.Add(7 * time.Hour)
	state := &suppressionState{decision: redisstate.ClipDecision{Count: 1, Threshold: 2, CounterID: strings.Repeat("b", 64), Epoch: event.EventID, EvaluatedAtMillis: at.UnixMilli()}}
	suppressor := &Suppressor{Catalog: catalog, Targets: suppressionTargets{}, State: state}
	repo := memory.New()
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enricher, nil, severity, &policyTestClock{at: at}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(snapshotter), lifecycle.WithNewAlertSuppressor(suppressor))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := processAggregation(t.Context(), repo, processor, event)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Processing.State != domain.EventProcessStateSuppressed || len(state.requests) != 1 || state.requests[0].PolicyID != row.ID {
		t.Fatalf("frozen matching rule lost during enrich: %+v requests=%+v", saved.Processing, state.requests)
	}
	// 外部映射已经改变，原共享对象跟随新配置；浅复制仍独立保留旧映射。
	latest, err := snapshotter.Snapshot(t.Context(), saved.Event, at)
	if err != nil || len(latest.Releases) != 0 {
		t.Fatalf("shared snapshotter unexpectedly mutated: %+v %v", latest, err)
	}
	old, err := scoped.Snapshot(t.Context(), saved.Event, at)
	if err != nil || len(old.Releases) != 1 || old.Releases[0].ID != row.ID {
		t.Fatalf("scoped snapshotter lost frozen mapping: %+v %v", old, err)
	}
}

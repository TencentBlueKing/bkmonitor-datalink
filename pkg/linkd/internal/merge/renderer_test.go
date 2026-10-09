// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/policy"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func renderSource() eventsource.Release {
	source := config.EventSource{EventSourceID: domain.BuiltinMergeEventSourceID, Version: 7, Enabled: true, Storage: config.EventSourceStorageConfig{Type: config.StorageTypeInternalMerge}}.WithDefaults()
	return eventsource.Release{ID: source.EventSourceID, Version: 7, Spec: source}
}

func renderFixture(t *testing.T) (Decision, []Snapshot, eventsource.Release, runtimeconfig.Snapshot) {
	t.Helper()
	d := decisionFixture(t)
	var spec policy.MergeSpec
	if err := json.Unmarshal(d.Policy.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Fields = []string{"bk_biz_id"}
	spec.AlarmTags = []int64{9, 2}
	spec.Template = []policy.TemplateField{{Key: "name", Value: "联合告警 ${alarm_num}"}, {Key: "level", Value: "fatal"}, {Key: "content", Value: "${cw_merged_content} ${cw_merged_meta_info}"}, {Key: "bk_biz_id", Value: "${bk_biz_id}"}, {Key: "item", Value: "联合故障"}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(policy.Merge, raw)
	if err != nil {
		t.Fatal(err)
	}
	d.Policy.Spec, d.Policy.Compiled = c.Canonical, c.Summary
	d.Progress = Progress{Phase: "capturing", UpdatedAt: d.FrozenAt}
	var members []Snapshot
	for _, id := range d.MemberIDs {
		alert := storetest.Alert(d.TenantID, id, "event-"+id, "fp-"+id, "warning")
		alert.Content = id + " ${alarm_num}"
		alert.ExtraData["bk_biz_id"] = json.RawMessage(`2`)
		alert.ExtraData["meta_info"] = json.RawMessage(`{"z":true,"a":[1,false]}`)
		alert.ExtraData["source_name"] = json.RawMessage(`"original source"`)
		alert.ExtraData["metric_unique_id"] = json.RawMessage(`"child metric"`)
		members = append(members, Snapshot{TenantID: d.TenantID, DecisionID: d.ID, Alert: alert})
	}
	return d, members, renderSource(), runtimeconfig.Snapshot{Severity: config.DefaultSeverityConfig()}
}

func TestRenderParentUsesFrozenFactsAndExplicitSource(t *testing.T) {
	d, members, source, severity := renderFixture(t)
	event, err := renderParent(t.Context(), d, members, source, severity)
	if err != nil {
		t.Fatal(err)
	}
	if event.Title != "联合告警 2" || event.Content != `a ${alarm_num}###b ${alarm_num} {"a":[1,false],"z":true}` || event.Evaluations[0].Severity != "critical" || event.EventSourceVersion != 7 || event.EventSourceID != domain.BuiltinMergeEventSourceID {
		t.Fatalf("unexpected parent %+v", event)
	}
	if string(event.ExtraData["bk_biz_id"]) != "2" || string(event.ExtraData["source_name"]) != `"告警合并"` || string(event.ExtraData["display_name"]) != `"联合故障"` || len(event.ExtraData["metric_unique_id"]) != 0 || len(event.Dimensions) != 0 || len(event.SourceRawData) != 0 || event.SubjectID != "" || event.EnrichStatus != domain.EnrichStatusPending || len(event.Enrich.Evaluations) != 0 {
		t.Fatal("parent inherited arbitrary member facts")
	}
	if !slices.Equal(event.MergeOrigin.AlarmTags, []int64{2, 9}) || !event.OccurredAt.Equal(d.FrozenAt) || event.SourceEventID != d.ID || event.SourceAlertID != event.Fingerprint {
		t.Fatal("unstable parent provenance")
	}
	again, err := renderParent(t.Context(), d, members, source, severity)
	if err != nil || !reflect.DeepEqual(event, again) {
		t.Fatal("render nondeterministic", err)
	}
	clone := event.Clone()
	clone.MergeOrigin.AlarmTags[0] = 8
	if event.MergeOrigin.AlarmTags[0] != 2 {
		t.Fatal("origin tags share memory")
	}
	if err := domain.ValidateEventRedelivery(clone, event); err == nil {
		t.Fatal("tag facts mutable on replay")
	}
	// 渲染不按当前时间移动身份，也不写入任何子来源/子处理器配置。
	d.Progress.ParentEvent = &event
	d.Progress.CaptureOffset = len(d.MemberIDs)
	d.Progress.Phase = "prepared"
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.Progress.ParentEvent.MergeOrigin.AlarmTags = []int64{3}
	if err := d.Validate(); err == nil {
		t.Fatal("policy tag mismatch accepted")
	}
}

func TestRenderParentKeepsDeclaredCustomOutputForKACProjection(t *testing.T) {
	d, members, source, severity := renderFixture(t)
	var spec policy.MergeSpec
	if err := json.Unmarshal(d.Policy.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec.FieldMappings = map[string]policy.FieldMapping{"owner": {Path: "$.extra_data.owner", Kind: policy.FieldText}}
	spec.Template = append(spec.Template, policy.TemplateField{Key: "owner", Value: "alice"})
	raw, _ := json.Marshal(spec)
	compiled, err := policy.Compile(policy.Merge, raw)
	if err != nil {
		t.Fatal(err)
	}
	d.Policy.Spec, d.Policy.Compiled = compiled.Canonical, compiled.Summary
	event, err := renderParent(t.Context(), d, members, source, severity)
	if err != nil {
		t.Fatal(err)
	}
	if string(event.ExtraData["__kac_custom_fields"]) != `["owner"]` {
		t.Fatal("parent custom output directory missing", event.ExtraData)
	}
	alert := storetest.Alert(d.TenantID, "parent", event.EventID, event.Fingerprint, "warning")
	alert.Content = "parent content"
	alert.ExtraData = event.ExtraData.Clone()
	payload, err := kachook.CompatibilityPayload(alert, severity.KACLevel)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(payload, &fields) != nil || fields["owner"] != "alice" {
		t.Fatal("parent custom output lost in projection", string(payload))
	}
}

func TestRenderParentRejectsScopeAndInvalidOutputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Decision, []Snapshot, *eventsource.Release, *runtimeconfig.Snapshot)
		want error
	}{
		{"foreign tenant", func(_ *Decision, m []Snapshot, _ *eventsource.Release, _ *runtimeconfig.Snapshot) {
			m[0].Alert.BKTenantID = "foreign"
		}, policy.ErrAccess},
		{"foreign operation", func(_ *Decision, m []Snapshot, _ *eventsource.Release, _ *runtimeconfig.Snapshot) {
			m[0].DecisionID = strings.Repeat("f", 64)
		}, policy.ErrAccess},
		{"unordered or duplicate members", func(_ *Decision, m []Snapshot, _ *eventsource.Release, _ *runtimeconfig.Snapshot) { m[1] = m[0] }, policy.ErrInvalid},
		{"source version mismatch", func(_ *Decision, _ []Snapshot, s *eventsource.Release, _ *runtimeconfig.Snapshot) { s.Version++ }, policy.ErrInvalid},
		{"external source", func(_ *Decision, _ []Snapshot, s *eventsource.Release, _ *runtimeconfig.Snapshot) {
			s.Spec.Storage.Type = "kafka"
		}, policy.ErrInvalid},
		{"source tenant mismatch", func(_ *Decision, _ []Snapshot, s *eventsource.Release, _ *runtimeconfig.Snapshot) {
			s.Spec.RelatedTenantID = "foreign"
		}, policy.ErrAccess},
		{"source disabled", func(_ *Decision, _ []Snapshot, s *eventsource.Release, _ *runtimeconfig.Snapshot) {
			s.Spec.Enabled = false
		}, policy.ErrUnavailable},
		{"source deleted", func(_ *Decision, _ []Snapshot, s *eventsource.Release, _ *runtimeconfig.Snapshot) { s.Deleted = true }, policy.ErrUnavailable},
		{"failed enrichment", func(_ *Decision, m []Snapshot, _ *eventsource.Release, _ *runtimeconfig.Snapshot) {
			m[0].Alert.EnrichStatus = domain.EnrichStatusFailed
			m[0].Alert.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"display":{"status":"failed","patches":[]}}]`)}
		}, policy.ErrUnavailable},
		{"not a valid standard severity", func(_ *Decision, _ []Snapshot, _ *eventsource.Release, s *runtimeconfig.Snapshot) {
			s.NativeNames = true
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, m, s, levels := renderFixture(t)
			tc.edit(&d, m, &s, &levels)
			_, err := renderParent(t.Context(), d, m, s, levels)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	d, m, s, levels := renderFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := renderParent(ctx, d, m, s, levels); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := renderParent(t.Context(), d, m[:1], s, levels); err == nil {
		t.Fatal("partial members accepted")
	}
}

func TestMergeSeverityAliasesMustBeUnambiguous(t *testing.T) {
	levels := runtimeconfig.Snapshot{Severity: config.DefaultSeverityConfig()}
	for input, want := range map[string]string{"fatal": "critical", "critical": "critical", "remind": "info", "info": "info", "warning": "warning"} {
		got, err := mergeSeverity(input, levels)
		if err != nil || got != want {
			t.Fatal(input, got, err)
		}
	}
	levels.Severity.Levels = append(levels.Severity.Levels, config.SeverityLevel{Name: "fatal", Priority: 4})
	if _, err := mergeSeverity("fatal", levels); err == nil {
		t.Fatal("ambiguous alias selected")
	}
	levels.NativeNames = true
	if got, err := mergeSeverity("fatal", levels); err != nil || got != "fatal" {
		t.Fatal("native name lost", err)
	}
}

func TestJournalPreparesRenderedEventOnceAndLifecycleCopiesTags(t *testing.T) {
	j, docs := newJournal(t)
	d, members, source, severity := renderFixture(t)
	if _, err := j.Claim(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := j.RenderAndPrepare(t.Context(), d.TenantID, d.ID, source, severity, d.FrozenAt); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("partial capture rendered", err)
	}
	for _, m := range members {
		if _, err := j.Capture(t.Context(), d.TenantID, d.ID, m.Alert); err != nil {
			t.Fatal(err)
		}
	}
	docs.failKind = "merge_decisions"
	if _, err := j.RenderAndPrepare(t.Context(), d.TenantID, d.ID, source, severity, d.FrozenAt); err == nil {
		t.Fatal("prepare storage failure swallowed")
	}
	prepared, err := j.RenderAndPrepare(t.Context(), d.TenantID, d.ID, source, severity, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	// 配置后来停用、等级改变、成员更新均不能覆盖已经准备好的业务事实。
	source.Spec.Enabled = false
	severity.NativeNames = true
	replay, err := j.RenderAndPrepare(t.Context(), d.TenantID, d.ID, source, severity, d.FrozenAt.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(prepared, replay) {
		t.Fatal("prepared event changed on retry", err)
	}
	repo := memory.New()
	created, err := repo.CreateEvent(t.Context(), *prepared.Decision.Progress.ParentEvent)
	if err != nil {
		t.Fatal(err)
	}
	action := &actionCounter{}
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), journalClock{at: d.FrozenAt.Add(time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessEvent(t.Context(), created.StoredEvent)
	if err != nil || len(result.AlertIDs) != 1 {
		t.Fatal("real lifecycle failed", err)
	}
	parent, err := repo.GetAlert(t.Context(), d.TenantID, result.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(parent.Alert.PolicyTags, []int64{2, 9}) || parent.Alert.Merge == nil || parent.Alert.Merge.RelationsReady || action.calls != 0 {
		t.Fatal("parent tags or relation gate incorrect")
	}
}

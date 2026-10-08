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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func aggregationRelease(t *testing.T) policy.Release {
	t.Helper()
	release := clipRelease(t)
	// 使用结构化配置替换方案，避免依赖 canonical JSON 的属性顺序。
	var spec policy.SuppressionSpec
	if err := json.Unmarshal(release.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Schemes = []policy.Scheme{{Type: "aggregation", Duration: 60, DurationType: "second", Fields: []string{"name"}}}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Suppression, encoded)
	if err != nil {
		t.Fatal(err)
	}
	release.Spec = compiled.Canonical
	release.Compiled = compiled.Summary
	return release
}

func aggregationProcessor(t *testing.T) (*lifecycle.Processor, *memory.Repository, *Suppressor, *policyTestClock) {
	t.Helper()
	state := realSuppressionState(t)
	release := aggregationRelease(t)
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := &Suppressor{Releases: &suppressionReleaseReader{release: release}, Targets: suppressionTargets{}, State: state, Aggregation: state, CurrentAlert: repo.GetAlert, NewAlertID: lifecycle.DeterministicAlertIDGenerator{}.Generate}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(fixedPolicySnapshot{release}), lifecycle.WithNewAlertSuppressor(s))
	if err != nil {
		t.Fatal(err)
	}
	return p, repo, s, clock
}

func aggregationEvent(id, source, fp string, at time.Time) domain.Event {
	event := storetest.Event("tenant", id, fp, "warning")
	event.EventSourceID = source
	event.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
	event.Labels["source_id"] = domain.NewStringScalar("source")
	event.OccurredAt = at
	event.ProducedAt = at
	event.ReceivedAt = at
	event.CreateAt = at
	return event
}

func processAggregation(ctx context.Context, repo store.Repository, p *lifecycle.Processor, event domain.Event) (store.StoredEvent, error) {
	created, err := repo.CreateEvent(ctx, event)
	if err != nil {
		return store.StoredEvent{}, err
	}
	if _, err := p.ProcessEvent(ctx, created.StoredEvent); err != nil {
		return store.StoredEvent{}, err
	}
	return repo.GetEvent(ctx, event.BKTenantID, event.EventID)
}

func mustProcessAggregation(t *testing.T, repo store.Repository, p *lifecycle.Processor, event domain.Event) store.StoredEvent {
	t.Helper()
	result, err := processAggregation(t.Context(), repo, p, event)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRedisAggregationLifecycleCrossSourceAndRecheckTerminalMain(t *testing.T) {
	p, repo, s, clock := aggregationProcessor(t)
	first := mustProcessAggregation(t, repo, p, aggregationEvent("first", "source-a", "fp-a", clock.at))
	if first.Processing.State != domain.EventProcessStateAccepted {
		t.Fatalf("first not admitted %+v", first.Processing)
	}
	second := mustProcessAggregation(t, repo, p, aggregationEvent("second", "source-b", "fp-b", clock.at))
	if second.Processing.State != domain.EventProcessStateSuppressed || second.Processing.ReasonCode != "aggregation_suppressed" || second.Event.RelatedAlertIDs[0] != first.Event.RelatedAlertIDs[0] {
		t.Fatalf("cross-source not suppressed %+v", second)
	}
	if _, err := repo.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: "tenant", EventSourceID: "source-b", Fingerprint: "fp-b"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("suppressed event created Alert")
	}
	// 活动主 Alert 的新触发即使无法读取任何聚合状态也继续更新。
	aggregation := s.Aggregation
	s.Aggregation = nil
	repeated := mustProcessAggregation(t, repo, p, aggregationEvent("repeat", "source-a", "fp-a", clock.at))
	if repeated.Processing.PolicyDecision.Suppression.BypassReason != "active_alert" {
		t.Fatal("active alert entered aggregation")
	}
	s.Aggregation = aggregation
	// 模拟终态写入已成功而清理提示丢失；新来源必须实时发现旧主已结束并竞争新窗口。
	main, err := repo.GetAlert(t.Context(), "tenant", first.Event.RelatedAlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	ended := main.Alert.Clone()
	ended.Status = domain.AlertStatusRecovered
	at := clock.at.Add(time.Second)
	ended.EndAt = &at
	ended.EndType = domain.AlertEndTypeSource
	ended.EndReason = "recovered"
	ended.UpdateAt = at
	if _, err := repo.CompareAndSetAlert(t.Context(), "tenant", ended.AlertID, main.Version, ended); err != nil {
		t.Fatal(err)
	}
	clock.at = at
	next := mustProcessAggregation(t, repo, p, aggregationEvent("next", "source-c", "fp-c", clock.at))
	if next.Processing.State != domain.EventProcessStateAccepted || next.Event.RelatedAlertIDs[0] == first.Event.RelatedAlertIDs[0] {
		t.Fatal("terminal owner retained suppression")
	}
	following := mustProcessAggregation(t, repo, p, aggregationEvent("following", "source-d", "fp-d", clock.at))
	if following.Event.RelatedAlertIDs[0] != next.Event.RelatedAlertIDs[0] || following.Processing.State != domain.EventProcessStateSuppressed {
		t.Fatal("replacement owner not registered")
	}
}

func TestRedisAggregationLifecycleConcurrentSourcesHaveOneMain(t *testing.T) {
	p, repo, _, clock := aggregationProcessor(t)
	type result struct {
		saved store.StoredEvent
		err   error
	}
	results := make(chan result, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			event := aggregationEvent(fmt.Sprintf("event-%d", i), fmt.Sprintf("source-%d", i), fmt.Sprintf("fp-%d", i), clock.at)
			saved, err := processAggregation(t.Context(), repo, p, event)
			results <- result{saved, err}
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	main := ""
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if len(r.saved.Event.RelatedAlertIDs) != 1 {
			t.Fatalf("missing aggregation association %+v", r.saved)
		}
		if main == "" {
			main = r.saved.Event.RelatedAlertIDs[0]
		} else if r.saved.Event.RelatedAlertIDs[0] != main {
			t.Fatal("multiple main alerts from race")
		}
		if r.saved.Processing.State == domain.EventProcessStateAccepted {
			accepted++
		} else if r.saved.Processing.State != domain.EventProcessStateSuppressed {
			t.Fatal("unexpected result")
		}
	}
	if accepted != 1 {
		t.Fatalf("created %d main alerts", accepted)
	}
}

func TestRedisAggregationLifecycleOldCloseKeepsNextFixedWindow(t *testing.T) {
	p, repo, _, clock := aggregationProcessor(t)
	first := mustProcessAggregation(t, repo, p, aggregationEvent("first", "source-a", "fp-a", clock.at))
	clock.at = clock.at.Add(61 * time.Second)
	second := mustProcessAggregation(t, repo, p, aggregationEvent("second", "source-b", "fp-b", clock.at))
	if second.Processing.State != domain.EventProcessStateAccepted {
		t.Fatal("window was extended")
	}
	if _, err := p.CloseAlert(t.Context(), lifecycle.CloseAlertCommand{OperationID: "close-old", BKTenantID: "tenant", AlertID: first.Event.RelatedAlertIDs[0], OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "closed", EffectiveAt: clock.at}); err != nil {
		t.Fatal(err)
	}
	third := mustProcessAggregation(t, repo, p, aggregationEvent("third", "source-c", "fp-c", clock.at))
	if third.Processing.State != domain.EventProcessStateSuppressed || third.Event.RelatedAlertIDs[0] != second.Event.RelatedAlertIDs[0] {
		t.Fatal("old alert close removed new window")
	}
}

type multiplePolicySnapshot struct{ releases []policy.Release }

func (s multiplePolicySnapshot) Snapshot(_ context.Context, _ domain.Event, at time.Time) (*store.PolicyContext, error) {
	result := &store.PolicyContext{EvaluatedAt: at}
	for _, release := range s.releases {
		result.Releases = append(result.Releases, store.PolicyReleaseRef{Kind: string(release.Kind), ID: release.ID, Version: release.Version, Digest: release.Compiled.Digest})
	}
	return result, nil
}

type multipleReleaseReader struct{ releases []policy.Release }

func (r multipleReleaseReader) GetRelease(_ context.Context, scope policy.Scope, id string, version int64) (policy.Release, error) {
	for _, release := range r.releases {
		if release.Scope == scope && release.ID == id && release.Version == version {
			return release, nil
		}
	}
	return policy.Release{}, policy.ErrNotFound
}

func TestRedisAggregationReleasesCandidateWhenLaterPolicySuppresses(t *testing.T) {
	state := realSuppressionState(t)
	aggregate := aggregationRelease(t)
	aggregate.ID = "aggregate"
	clip := clipRelease(t)
	var spec policy.SuppressionSpec
	if err := json.Unmarshal(clip.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Policy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"level","target_value":"fatal"}}`)
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Suppression, raw)
	if err != nil {
		t.Fatal(err)
	}
	clip.Spec = compiled.Canonical
	clip.Compiled = compiled.Summary
	releases := []policy.Release{aggregate, clip}
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := &Suppressor{Releases: multipleReleaseReader{releases}, Targets: suppressionTargets{}, State: state, Aggregation: state, CurrentAlert: repo.GetAlert, NewAlertID: lifecycle.DeterministicAlertIDGenerator{}.Generate}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(multiplePolicySnapshot{releases}), lifecycle.WithNewAlertSuppressor(s))
	if err != nil {
		t.Fatal(err)
	}
	firstEvent := aggregationEvent("blocked", "source-a", "fp-a", clock.at)
	firstEvent.Evaluations[0].Severity = "critical"
	first := mustProcessAggregation(t, repo, p, firstEvent)
	if first.Processing.State != domain.EventProcessStateSuppressed || first.Processing.PolicyDecision.Suppression.Evaluations[0].Steps[0].Outcome != "released" {
		t.Fatalf("suppressed candidate retained qualification %+v", first.Processing)
	}
	next := mustProcessAggregation(t, repo, p, aggregationEvent("allowed", "source-b", "fp-b", clock.at))
	if next.Processing.State != domain.EventProcessStateAccepted || next.Processing.PolicyDecision.Suppression.Evaluations[0].Steps[0].Outcome != "reserved" {
		t.Fatalf("later source blocked by old reservation %+v", next.Processing)
	}
}

func TestRedisAggregationRuntimeRejectsForeignMain(t *testing.T) {
	p, repo, s, clock := aggregationProcessor(t)
	mustProcessAggregation(t, repo, p, aggregationEvent("first", "source-a", "fp-a", clock.at))
	s.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		a, err := repo.GetAlert(ctx, tenant, id)
		a.Alert.BKTenantID = "foreign"
		return a, err
	}
	if _, err := processAggregation(t.Context(), repo, p, aggregationEvent("second", "source-b", "fp-b", clock.at)); !errors.Is(err, policy.ErrAccess) {
		t.Fatal("foreign main became ordinary skip", err)
	}
}

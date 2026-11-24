// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func withCurrentOutput(event domain.Event, status domain.EnrichStatus) domain.Event {
	event.EventEnrichment = storetest.Enrichment(event)
	event.EnrichStatus = status
	event.Enrich.Evaluations[0].Status = status
	event.Enrich.Evaluations[0].Data = domain.JSONObject{"processors": json.RawMessage(`[{"source":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.source_name","value":"betty-推送-克隆"}]}},{"cmdb":{"status":"` + string(status) + `","patches":[],"diagnostics":[{"code":"missing_field"}]}}]`)}
	if status == domain.EnrichStatusFailed {
		event.Labels["source_name"] = domain.NewStringScalar("betty-推送-克隆")
		event.Enrich.Evaluations[0].Data["processors"] = json.RawMessage(`[{"cmdb":{"status":"failed","patches":[],"diagnostics":[{"code":"missing_field"}]}}]`)
	}
	event.Title = "host-CPU使用率过高-时间屏蔽"
	delete(event.Dimensions, "bk_biz_id")
	event.ExtraData["bk_biz_id"] = json.RawMessage(`2`)
	return event
}

func currentOutputRelease(t *testing.T, release policy.Release) policy.Release {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(release.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	condition := json.RawMessage(`{"expression":"A and B","A":{"condition":"term","target_key":"source_name","target_value":"betty-推送-克隆"},"B":{"condition":"regexp","target_key":"name","target_value":"时间屏蔽"}}`)
	spec["policy"] = condition
	if release.Kind == policy.Merge {
		spec["policy"] = []json.RawMessage{condition, condition}
		spec["aggregate_fields"] = []string{"source_name"}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(release.Kind, raw)
	if err != nil {
		t.Fatal(err)
	}
	release.Spec, release.Compiled = compiled.Canonical, compiled.Summary
	return release
}

func TestTimeShieldCurrentOutputBindsAndRechecksAfterEnrichFailure(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		t.Run(string(status), func(t *testing.T) {
			repo := memory.New()
			clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
			release := currentOutputRelease(t, timeShieldRelease(t))
			action := &shieldHook{}
			catalog := policy.NewCatalog(&shieldDirectory{releases: []policy.Release{release}})
			shielder := &Shielder{Loader: &Suppressor{Catalog: catalog, Targets: shieldTargets{}}, Events: repo.GetEvent}
			enricher := snapshotEnricherFunc(func(_ context.Context, input enrich.Input) (enrich.Result, error) {
				output := withCurrentOutput(input.Event, status)
				return enrich.Result{Status: output.EnrichStatus, Data: output.Enrich, ConfigDigest: "current-output", ChainKind: enrich.ChainConfigured}, nil
			})
			processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enricher, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: catalog}), lifecycle.WithShieldEvaluator(shielder))
			if err != nil {
				t.Fatal(err)
			}
			event := shieldEvent("current-output", clock)
			event.Title = "host-CPU使用率过高-时间屏蔽"
			delete(event.Dimensions, "bk_biz_id")
			event.ExtraData["bk_biz_id"] = json.RawMessage(`2`)
			if status == domain.EnrichStatusFailed {
				event.Labels["source_name"] = domain.NewStringScalar("betty-推送-克隆")
			}
			result := mustProcessAggregation(t, repo, processor, event)
			if len(result.Event.RelatedAlertIDs) != 1 {
				t.Fatal("missing alert", result.Processing)
			}
			current, err := repo.GetAlert(t.Context(), "tenant", result.Event.RelatedAlertIDs[0])
			if err != nil || !current.Alert.Shield.Active || len(current.Alert.Shield.Bindings) != 1 || len(action.calls) != 0 {
				t.Fatal("shield did not bind", current.Alert.Shield, err)
			}
			if changed, err := processor.CheckShield(t.Context(), "tenant", current.Alert.AlertID); err != nil || changed {
				t.Fatal("matching original event lost binding", changed, err)
			}
		})
	}
}

func TestSuppressionCurrentOutputReachesClipAfterEnrichFailure(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		t.Run(string(status), func(t *testing.T) {
			suppressor, event, snapshot, state := suppressionFixture(t)
			release := currentOutputRelease(t, clipRelease(t))
			suppressor.Releases = &suppressionReleaseReader{release: release}
			snapshot.Releases[0].Digest = release.Compiled.Digest
			event = withCurrentOutput(event, status)
			result, err := suppressor.Check(t.Context(), event, event.Evaluations[0], snapshot, nil)
			if err != nil || !result.Suppressed || result.ReasonCode != "clip_below_threshold" || len(state.requests) != 1 {
				t.Fatalf("result=%+v requests=%d err=%v", result, len(state.requests), err)
			}
		})
	}
}

type currentOutputAggregationState struct {
	AggregationState
	requests []redisstate.AggregationRequest
}

func (s *currentOutputAggregationState) ClaimAggregation(_ context.Context, request redisstate.AggregationRequest) (redisstate.AggregationDecision, error) {
	s.requests = append(s.requests, request)
	return redisstate.AggregationDecision{Role: "candidate", WindowID: strings.Repeat("b", 64), Epoch: request.EventID, OwnerAlertID: request.CandidateAlertID, OwnerEventID: request.EventID, OwnerSourceID: request.SourceID, OwnerFingerprint: request.Fingerprint, StartedAtMillis: request.At.UnixMilli(), ExpiresAtMillis: request.At.Add(request.Duration).UnixMilli()}, nil
}

func TestAggregationCurrentOutputReachesReservationAfterEnrichFailure(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		t.Run(string(status), func(t *testing.T) {
			suppressor, event, snapshot, _ := suppressionFixture(t)
			release := currentOutputRelease(t, aggregationRelease(t))
			suppressor.Releases = &suppressionReleaseReader{release: release}
			snapshot.Releases[0].Digest = release.Compiled.Digest
			event = withCurrentOutput(event, status)
			state := &currentOutputAggregationState{}
			suppressor.Aggregation = state
			suppressor.NewAlertID = func(domain.Event) (string, error) { return "candidate", nil }
			suppressor.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) {
				t.Fatal("new reservation must not read an existing owner")
				return store.StoredAlert{}, nil
			}
			result, err := suppressor.Check(t.Context(), event, event.Evaluations[0], snapshot, nil)
			if err != nil || len(state.requests) != 1 || len(result.Steps) != 1 || result.Steps[0].Outcome != "reserved" {
				t.Fatalf("result=%+v requests=%d err=%v", result, len(state.requests), err)
			}
		})
	}
}

// 状态端口只记录测试专属窗口，验证正式合并匹配和分组确实进入入窗步骤。
type currentOutputMergeState struct {
	requests []redisstate.MergeRequest
	wait     domain.MergeWait
}

func (s *currentOutputMergeState) JoinMergeWindow(_ context.Context, r redisstate.MergeRequest) (domain.MergeWait, error) {
	s.requests = append(s.requests, r)
	s.wait = domain.MergeWait{WindowID: strings.Repeat("a", 64), Policy: r.Policy, GroupKey: r.GroupKey, MemberEventID: r.Member.EventID, Severity: r.Member.Severity, Groups: r.Groups, StartedAt: r.At, Deadline: r.At.Add(r.Duration)}
	return s.wait, nil
}
func (*currentOutputMergeState) CommitMergeMember(context.Context, string, string, domain.MergeWait) (bool, error) {
	return true, nil
}
func (s *currentOutputMergeState) ReadMergeWindowMeta(context.Context, string, string) (redisstate.MergeWindow, bool, error) {
	return redisstate.MergeWindow{Policy: s.wait.Policy, GroupKey: s.wait.GroupKey}, true, nil
}

func TestMergeCurrentOutputReachesWindowAfterEnrichFailure(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		t.Run(string(status), func(t *testing.T) {
			release := currentOutputRelease(t, mergeRelease(t))
			directory := &shieldDirectory{releases: []policy.Release{release}}
			catalog := policy.NewCatalog(directory)
			event := withCurrentOutput(aggregationEvent("current-output", "source", "fp", time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)), status)
			snapshot, err := (Snapshotter{Catalog: catalog}).Snapshot(t.Context(), event, event.CreateAt)
			if err != nil || len(snapshot.Releases) != 1 {
				t.Fatal("candidate filtering lost current fields", snapshot, err)
			}
			state := &currentOutputMergeState{}
			merger := &Merger{Loader: &Suppressor{Releases: directory, Catalog: catalog, Targets: suppressionTargets{}}, State: state}
			alert := storetest.Alert("tenant", "a1", event.EventID, event.Fingerprint, "warning")
			alert.EventSourceID = event.EventSourceID
			result, err := merger.Check(t.Context(), event, event.Evaluations[0], snapshot, alert, nil)
			if err != nil || result.Merge == nil || result.Merge.State != "pending" || len(state.requests) != 1 {
				t.Fatalf("result=%+v requests=%d err=%v", result, len(state.requests), err)
			}
			want, err := policy.GroupKey([]any{"betty-推送-克隆"})
			if err != nil || state.requests[0].GroupKey != want || len(state.requests[0].Groups) != 2 {
				t.Fatal("current source grouping lost", state.requests, err)
			}
		})
	}
}

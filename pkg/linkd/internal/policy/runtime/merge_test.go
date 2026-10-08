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
	"reflect"
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

func mergeRelease(t *testing.T) policy.Release {
	t.Helper()
	raw := json.RawMessage(`{"name":"merge","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":[{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"db"}},{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"app"}}],"merge_cycle":60,"is_cycle_merge":false,"aggregate_fields":[],"new_alarm_config":[{"key":"name","value":"joint ${alarm_num}"},{"key":"level","value":"warning"}]}`)
	c, err := policy.Compile(policy.Merge, raw)
	if err != nil {
		t.Fatal(err)
	}
	return policy.Release{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Merge}, ID: "merge", Version: 1, Spec: c.Canonical, Compiled: c.Summary}
}

func TestRedisMergeLifecycleBlocksActionPreservesOpeningAndEndsWaiting(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state := realSuppressionState(t)
	directory := &shieldDirectory{releases: []policy.Release{mergeRelease(t)}}
	secondPolicy := directory.releases[0]
	secondPolicy.ID = "merge-second"
	directory.releases = append(directory.releases, secondPolicy)
	catalog := policy.NewCatalog(directory)
	loader := &Suppressor{Catalog: catalog, Releases: directory, Targets: shieldTargets{}}
	merger := &Merger{Loader: loader, State: state}
	stateHook, action := &shieldHook{}, &shieldHook{}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: stateHook}, {Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: catalog}), lifecycle.WithMergeEvaluator(merger))
	if err != nil {
		t.Fatal(err)
	}
	db := dependencyEvent("db", "source-a", "db", clock)
	first := mustProcessAggregation(t, repo, p, db)
	id := first.Event.RelatedAlertIDs[0]
	stored, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Alert.Merge == nil || stored.Alert.Merge.State != "pending" || len(stored.Alert.Merge.Pending) != 2 || stored.Alert.Admission.AdmittedAt != nil || len(action.calls) != 0 || len(stateHook.calls) != 1 {
		t.Fatalf("merge did not gate action %+v", stored.Alert)
	}
	wait := stored.Alert.Merge.Pending[0]
	window, found, err := state.ReadMergeWindow(t.Context(), "tenant", wait.WindowID)
	if err != nil || !found || len(window.Members) != 1 || !window.Members[0].Committed {
		t.Fatal("real alert not confirmed", err)
	}
	clock.at = clock.at.Add(5 * time.Second)
	repeat := dependencyEvent("repeat", "source-a", "changed", clock)
	repeat.Fingerprint = db.Fingerprint
	mustProcessAggregation(t, repo, p, repeat)
	current, _ := repo.GetAlert(t.Context(), "tenant", id)
	if !reflect.DeepEqual(current.Alert.Merge.Pending[0], wait) || current.Alert.Title != "db" || len(action.calls) != 0 {
		t.Fatal("repeat refreshed wait or opening")
	}
	second := mustProcessAggregation(t, repo, p, dependencyEvent("app", "source-b", "app", clock))
	other, _ := repo.GetAlert(t.Context(), "tenant", second.Event.RelatedAlertIDs[0])
	if other.Alert.Merge == nil || other.Alert.Merge.Pending[0].WindowID != wait.WindowID {
		t.Fatal("cross source merge split")
	}
	judge := &MergeJudge{Loader: loader, Windows: state, CurrentAlert: repo.GetAlert}
	judged, err := judge.Evaluate(t.Context(), "tenant", wait.WindowID, clock.at, nil)
	if err != nil || !judged.Frozen || judged.Window.Frozen.Outcome != "succeeded" || len(judged.Members) != 2 {
		t.Fatal("independent judge did not freeze real members", err)
	}
	// Redis 的冻结仅为候选结果；没有真实父 Alert/关系时不得提前把子 Alert 标记 merged。
	unchanged, _ := repo.GetAlert(t.Context(), "tenant", id)
	if unchanged.Alert.Merge.State != "pending" || len(action.calls) != 0 {
		t.Fatal("window freeze prematurely completed business merge")
	}
	end := dependencyEvent("end", "source-a", "db", clock)
	end.Fingerprint = db.Fingerprint
	end.Evaluations[0].Action = domain.EventActionResolved
	mustProcessAggregation(t, repo, p, end)
	current, _ = repo.GetAlert(t.Context(), "tenant", id)
	if current.Alert.Status != domain.AlertStatusRecovered || current.Alert.Merge.State != "released" || len(current.Alert.Merge.Pending) != 0 || len(action.calls) != 0 {
		t.Fatal("terminal inherited waiting or dispatched unadmitted alert")
	}
	// 另一个策略窗口仍保留 Redis committed 成员，但真实已恢复的 DB 告警不能补齐组。
	remaining := other.Alert.Merge.Pending[1]
	j2, err := judge.Evaluate(t.Context(), "tenant", remaining.WindowID, clock.at, nil)
	if err != nil || j2.Frozen {
		t.Fatal("terminal Redis member caused early success", err)
	}
	judge.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) {
		return store.StoredAlert{}, errors.New("storage unavailable")
	}
	if _, err := judge.Evaluate(t.Context(), "tenant", remaining.WindowID, remaining.Deadline, nil); err == nil {
		t.Fatal("storage error became failed/succeeded verdict")
	}
	judge.CurrentAlert = repo.GetAlert
	expired, err := judge.Evaluate(t.Context(), "tenant", remaining.WindowID, remaining.Deadline, nil)
	if err != nil || !expired.Frozen || expired.Window.Frozen.Outcome != "failed" {
		t.Fatal("incomplete real members did not fail at deadline", err)
	}

}

func TestRedisShieldTimerDoesNotAutomaticallyEnterMergeWindow(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state := realSuppressionState(t)
	directory := &shieldDirectory{releases: []policy.Release{mergeRelease(t), timeShieldRelease(t)}}
	catalog := policy.NewCatalog(directory)
	loader := &Suppressor{Catalog: catalog, Releases: directory, Targets: shieldTargets{}}
	action := &shieldHook{}
	merger := &Merger{Loader: loader, State: state}
	shielder := &Shielder{Loader: loader, Events: repo.GetEvent}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: catalog}), lifecycle.WithShieldEvaluator(shielder), lifecycle.WithMergeEvaluator(merger))
	if err != nil {
		t.Fatal(err)
	}
	e := dependencyEvent("db", "source", "db", clock)
	first := mustProcessAggregation(t, repo, p, e)
	id := first.Event.RelatedAlertIDs[0]
	a, _ := repo.GetAlert(t.Context(), "tenant", id)
	if !a.Alert.Shield.Active || a.Alert.Merge != nil {
		t.Fatal("shielded member entered merge")
	}
	clock.at = clock.at.Add(11 * time.Second)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || !changed {
		t.Fatal(err)
	}
	a, _ = repo.GetAlert(t.Context(), "tenant", id)
	if a.Alert.Merge != nil || a.Alert.Admission.AdmittedAt != nil || len(action.calls) != 0 {
		t.Fatal("timer admitted or merged member")
	}
	again := dependencyEvent("trigger-after-timer", "source", "db", clock)
	again.Fingerprint = e.Fingerprint
	mustProcessAggregation(t, repo, p, again)
	a, _ = repo.GetAlert(t.Context(), "tenant", id)
	if a.Alert.Merge == nil || a.Alert.Merge.State != "pending" || len(action.calls) != 0 {
		t.Fatal("new trigger did not enter merge after unshield")
	}
}

type budgetMergeWindows struct {
	window  redisstate.MergeWindow
	freezes int
}

func (w *budgetMergeWindows) ReadMergeWindow(context.Context, string, string) (redisstate.MergeWindow, bool, error) {
	return w.window, true, nil
}

func (w *budgetMergeWindows) FreezeMergeWindow(context.Context, string, string, int64, time.Time, []redisstate.MergeCandidate) (redisstate.MergeWindow, bool, error) {
	w.freezes++
	return w.window, true, nil
}

func TestMergeJudgeRejectsOversizeSnapshotSetBeforeFreezing(t *testing.T) {
	release := mergeRelease(t)
	directory := &shieldDirectory{releases: []policy.Release{release}}
	loader := &Suppressor{Releases: directory, Catalog: policy.NewCatalog(directory), Targets: shieldTargets{}}
	at := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	group, err := policy.GroupKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	version := domain.PolicyVersion{ID: release.ID, Version: release.Version, Digest: release.Compiled.Digest}
	windows := &budgetMergeWindows{window: redisstate.MergeWindow{ID: strings.Repeat("a", 64), TenantID: "tenant", Policy: version, GroupKey: group, StartedAtMillis: at.UnixMilli(), DeadlineMillis: at.Add(time.Minute).UnixMilli(), GroupCount: 2, Revision: 1}}
	alerts := map[string]domain.Alert{}
	content := strings.Repeat("x", 512<<10)
	for i := range 65 {
		id := fmt.Sprintf("member-%02d", i)
		a := storetest.Alert("tenant", id, "event-"+id, id, "warning")
		a.Title = []string{"db", "app"}[i%2]
		a.Content = content
		a.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
		wait := domain.MergeWait{WindowID: windows.window.ID, Policy: version, GroupKey: group, MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{i % 2}, StartedAt: at, Deadline: at.Add(time.Minute)}
		a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{wait}}
		alerts[id] = a
		windows.window.Members = append(windows.window.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: id, EventID: a.TriggerEventID, EventSourceID: a.EventSourceID, Fingerprint: a.Fingerprint, Severity: a.Severity}, Groups: []int{i % 2}, FirstAtMillis: at.UnixMilli(), Committed: true})
	}
	judge := MergeJudge{Loader: loader, Windows: windows, CurrentAlert: func(_ context.Context, tenant, id string) (store.StoredAlert, error) {
		return store.StoredAlert{Alert: alerts[id], Version: store.NewVersionToken("1")}, nil
	}}
	if _, err := judge.Evaluate(t.Context(), "tenant", windows.window.ID, at, nil); !errors.Is(err, policy.ErrUnavailable) || windows.freezes != 0 {
		t.Fatal("oversized partial snapshots frozen", err)
	}
}

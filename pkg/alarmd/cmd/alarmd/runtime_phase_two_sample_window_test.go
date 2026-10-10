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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// sampleDirectory answers the directory as one process does: the Leader,
// which resolves the strategy's current row, or a follower, which holds no
// publication and resolves nothing.
type sampleDirectory struct {
	leader bool
	row    controlplane.StrategyDirectoryRow
}

func (d sampleDirectory) Available() bool { return d.leader }

func (d sampleDirectory) Page(context.Context, time.Time, string, string, string, int, int) controlplane.StrategyDirectorySnapshot {
	return controlplane.StrategyDirectorySnapshot{Complete: d.leader, Rows: []controlplane.StrategyDirectoryRow{}}
}

func (d sampleDirectory) ResolveCurrent(context.Context, time.Time, string, string, string, string, ...string) (controlplane.StrategyDirectoryRow, error) {
	if !d.leader {
		return controlplane.StrategyDirectoryRow{}, controlplane.ErrSnapshotUnavailable
	}
	return d.row, nil
}

func (sampleDirectory) EffectivePlan(context.Context, controlplane.StrategyDirectoryRow) (controlplane.QueryGroupPlanObject, error) {
	return controlplane.QueryGroupPlanObject{}, controlplane.ErrSnapshotUnavailable
}

func (sampleDirectory) EffectiveOutput(context.Context, controlplane.StrategyDirectoryRow) controlplane.OutputFormatFacts {
	return controlplane.OutputFormatFacts{}
}

// sampleReplica is one process's share of a sample window: its own window
// store and diagnostics over the shared Redis, its own sampler, its applier.
type sampleReplica struct {
	api     http.Handler
	sampler *observability.SeriesSampler
	applier observationWindowApplier
}

func newSampleReplica(t *testing.T, client redis.Cmdable, directory sampleDirectory, forward fleet.LeaderForward, now func() time.Time) *sampleReplica {
	t.Helper()
	const prefix = "alarmd-sample-two-replicas"
	windows, err := fleet.NewWindowStore(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	store, err := fleet.NewDiagnosticStore(client, prefix+":fleet")
	if err != nil {
		t.Fatal(err)
	}
	sampler, err := observability.NewSeriesSampler(observability.SeriesSampleLimits{RecordsPerMinute: 8, BytesPerMinute: 8 * observability.SeriesSampleMaxBytes, QueueCapacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := observability.NewTargetFlow(observability.New(observability.ComponentRuntime, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a sample open reached the plain route: %s", r.URL)
	})
	return &sampleReplica{api: fleet.WithSeriesSamples(next, directory, forward, windows, store, sampler, now), sampler: sampler,
		applier: observationWindowApplier{store: windows, flow: flow, samples: sampler, now: now}}
}

// The directory is the Leader's alone, and a sample window is opened
// against it; the series it names is evaluated by the replica that owns the
// Query Group, which is usually not the Leader. The window reaches that
// replica through the shared window store: a follower hands the open to
// the Leader, the Leader resolves the row and writes the window, and the
// owner, on its next pass, samples the series - a replica that did not
// apply the store yet does not.
func TestASampleWindowTheLeaderOpensIsSampledByTheOwningFollower(t *testing.T) {
	at := time.Now()
	now := func() time.Time { return at }
	queryGroup := strings.Repeat("a", 64)
	row := controlplane.StrategyDirectoryRow{
		Identity:   execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "4101"},
		QueryGroup: execution.QueryGroupIdentity(queryGroup), Role: string(execution.ActivationCurrent),
		Activation: &execution.PlanActivationFact{Selection: execution.ActivationCurrent,
			Selected: execution.ActivatedPlan{StateGeneration: "generation", ScheduleRevision: "schedule"}},
	}
	shared := windowRedis(t)
	leader := newSampleReplica(t, shared, sampleDirectory{leader: true, row: row}, nil, now)
	hops := 0
	forward := func(w http.ResponseWriter, r *http.Request) (bool, string) {
		hops++
		hop := r.Clone(r.Context())
		hop.Header.Set(fleet.ForwardedHeader(), "owner")
		leader.api.ServeHTTP(w, hop)
		return true, ""
	}
	owner := newSampleReplica(t, shared, sampleDirectory{}, forward, now)

	candidate := observability.SeriesSampleCandidate{QueryGroup: queryGroup, TenantID: "default", BusinessID: "2", StrategyID: "4101",
		StateGeneration: "generation", PlanScheduleRevision: "schedule", SeriesDigest: "series", Slot: 1}
	if reservation := owner.sampler.TryReserve(context.Background(), candidate); reservation != nil {
		reservation.Cancel()
		t.Fatal("the owner samples the series before any window was opened")
	}

	w := httptest.NewRecorder()
	owner.api.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/windows",
		strings.NewReader(`{"mode":"sample","strategy":"4101","series_digest":"series","ttl_seconds":60,"opened_by":"operator"}`)))
	if w.Code != http.StatusOK || hops != 1 || !strings.Contains(w.Body.String(), queryGroup) {
		t.Fatalf("open on the owner = %d %s after %d hops, want the Leader's window through one hop", w.Code, w.Body.String(), hops)
	}
	if reservation := owner.sampler.TryReserve(context.Background(), candidate); reservation != nil {
		reservation.Cancel()
		t.Fatal("the owner samples before its applier read the store: the window reached it some other way")
	}

	owner.applier.applyOnce(context.Background())
	reservation := owner.sampler.TryReserve(context.Background(), candidate)
	if reservation == nil {
		t.Fatalf("the owner does not sample the series after applying the window the Leader opened: %+v", owner.sampler.Health())
	}
	reservation.Cancel()
	// The window is the strategy's current Plan, as the Leader resolved it:
	// a candidate of another generation is not sampled.
	stale := candidate
	stale.StateGeneration = "before"
	if other := owner.sampler.TryReserve(context.Background(), stale); other != nil {
		other.Cancel()
		t.Fatal("the owner samples a Plan generation the Leader did not resolve")
	}
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The close paths of one strategy, together, through the production bundle:
// the inactive close (effective-time maintenance, running on the bundle's
// own loop) and the edit and absent close (built by their production
// constructors over the bundle's own control plane), all sending through
// the link's close by filter to one stub of the link's control plane, with
// one real redis-server.

const (
	closePathsTenant   = "tenant-a"
	closePathsBusiness = int64(2)
)

// closePathsA1 is an alert of the strategy the link holds open.
var closePathsA1 = strings.Repeat("a1", 16)

// closePathsFixture is the production bundle over the strategy, the stub
// link, and the edit and absent close built over the bundle's control plane.
type closePathsFixture struct {
	t       *testing.T
	life    *lifecycleFixture
	plane   *stubControlPlane
	control *controlplane.SourceReconciler
	absent  *absentStrategyClose
	// start is the time the strategy's effective window is laid around: in
	// effect from ten minutes before it to twenty minutes after.
	start time.Time
}

// startClosePathsFixture installs the strategy at revision 1, grouped by
// the host and device, without no-data, and opens the bundle with the
// link's control plane named.
func startClosePathsFixture(t *testing.T) *closePathsFixture {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	life := &lifecycleFixture{t: t, address: address, redis: client, interval: lifecycleInterval,
		clock: &atomic.Int64{}, sink: &followsSink{mode: followsOK}}
	base := time.Now().Unix()
	base += life.interval - base%life.interval
	life.base = base
	life.clock.Store(base * 1000)
	now := func() time.Time { return time.UnixMilli(life.clock.Load()) }
	fixture := &closePathsFixture{t: t, life: life, start: time.Unix(base, 0).UTC(), plane: &stubControlPlane{bodies: map[string][]string{}}}

	uqServer := httptest.NewServer(http.HandlerFunc(life.answerQuery))
	t.Cleanup(uqServer.Close)
	planeServer := httptest.NewServer(fixture.plane)
	t.Cleanup(planeServer.Close)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-close-paths"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Output.Protocol = config.OutputProtocolNative
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: planeServer.URL, JWTSecret: "example-secret"}
	life.cfg = cfg
	life.retry = cfg.PhaseTwo.Scheduler.RetryMinDelay.Duration()

	ctx := context.Background()
	for _, prefix := range []string{"", closePathsTenant + "."} {
		for field, record := range lifecycleHostRecords("") {
			if err := client.HSet(ctx, prefix+cfg.PlatformKeyPrefix()+".cache.cmdb.host", field, record).Err(); err != nil {
				t.Fatal(err)
			}
		}
		// The business's timezone, which its effective time is read in.
		if err := client.HSet(ctx, prefix+cfg.PlatformKeyPrefix()+".cache.cmdb.business", strconv.FormatInt(closePathsBusiness, 10),
			`{"bk_tenant_id":"`+closePathsTenant+`","time_zone":"UTC"}`).Err(); err != nil {
			t.Fatal(err)
		}
	}
	fixture.install(1, []string{"bk_target_ip", "bk_target_cloud_id", "device"}, false, nil)
	life.open("alarmd-worker-0", uqServer.Client())
	// The Worker's executable view carries the Query Group, which the
	// inactive close reads its Plans under.
	settleExecutableView(ctx, life.bundle, 10*time.Second)

	control, ok := life.bundle.dependencies.Control.(*productionPhaseTwoControl).dependencies.Reconciler.(*controlplane.SourceReconciler)
	if !ok {
		t.Fatalf("the bundle's reconciler is %T", life.bundle.dependencies.Control.(*productionPhaseTwoControl).dependencies.Reconciler)
	}
	fixture.control = control
	// As the bundle wires them (runtime_phase_two_bundle.go): the link's
	// client and closer from the same configuration, over the case's Redis.
	link, err := newLinkdManage(cfg, now)
	if err != nil || link == nil {
		t.Fatalf("link client %v (%v)", link, err)
	}
	closer, err := newFilterCloser(cfg, link, client, now, nil, nil)
	if err != nil || closer == nil {
		t.Fatalf("closer %v (%v)", closer, err)
	}
	route := newFilterRoute(cfg, closer)
	fixture.absent = newAbsentStrategyClose(life.bundle, control, route)
	fixture.absent.edits = newEditStrategyClose(life.bundle, control, route)
	return fixture
}

// install writes the strategy at revision: its grouping, whether it
// detects no-data (over the host), its filter conditions, and its effective
// window around fixture.start.
func (fixture *closePathsFixture) install(revision int, dimensions []string, noData bool, conditions []any) {
	t := fixture.t
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["update_time"] = 1725000000 + revision
	document["strategy_revision"] = revision
	document["name"] = "close paths"
	document["scenario"] = "os"
	item := document["items"].([]any)[0].(map[string]any)
	item["name"] = "CPU usage"
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"] = lifecycleInterval
	query["metric_id"] = "bk_monitor.system.cpu.usage"
	grouping := make([]any, 0, len(dimensions))
	for _, dimension := range dimensions {
		grouping = append(grouping, dimension)
	}
	query["agg_dimension"] = grouping
	if conditions == nil {
		conditions = []any{}
	}
	query["agg_condition"] = conditions
	if noData {
		item["no_data_config"] = map[string]any{"is_enabled": true, "continuous": 1, "level": noDataConfiguredLevelID,
			"agg_dimension": []any{"bk_target_ip", "bk_target_cloud_id"}}
	}
	window := map[string]any{"start": fixture.start.Add(-10 * time.Minute).Format("15:04"), "end": fixture.start.Add(20 * time.Minute).Format("15:04")}
	for _, detect := range document["detects"].([]any) {
		trigger := detect.(map[string]any)["trigger_config"].(map[string]any)
		trigger["check_window"] = 1
		trigger["uptime"] = map[string]any{"time_ranges": []any{window}, "calendars": []any{}}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":                    `[` + lifecycleStrategyID + `]`,
		"alarm-config.strategy_" + lifecycleStrategyID: encoded,
		"alarm-config.last_updated":                    strconv.Itoa(1725000000 + revision),
	} {
		if err := fixture.life.redis.Set(context.Background(), key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// publish installs the strategy and waits for the Leader's publication to
// carry its revision, and answers that publication's facts.
func (fixture *closePathsFixture) publish(revision int, dimensions []string, noData bool, conditions []any) controlplane.StrategyPlanFacts {
	t := fixture.t
	t.Helper()
	fixture.install(revision, dimensions, noData, conditions)
	ctx := context.Background()
	for deadline := testwait.Deadline(t); ; time.Sleep(20 * time.Millisecond) { // fixed-wait: the poll interval of a condition wait bounded by the test deadline
		if err := fixture.life.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("refresh after installing revision %d: %v", revision, err)
		}
		facts, available, found := fixture.control.StrategyPlanFacts(closePathsTenant, lifecycleStrategyID)
		if available && found && facts.Revision == int64(revision) {
			settleExecutableView(ctx, fixture.life.bundle, 10*time.Second)
			return facts
		}
		if time.Now().After(deadline) {
			t.Fatalf("the publication carries %+v (available %v, found %v) at the test's deadline, want revision %d", facts, available, found, revision)
		}
	}
}

// facts is the publication's current facts of the strategy.
func (fixture *closePathsFixture) facts() controlplane.StrategyPlanFacts {
	fixture.t.Helper()
	facts, _, found := fixture.control.StrategyPlanFacts(closePathsTenant, lifecycleStrategyID)
	if !found {
		fixture.t.Fatal("the publication has no Plan of the strategy")
	}
	return facts
}

// open makes the link hold the alerts open: every filter matches them
// until a close that is not a dry run.
func (fixture *closePathsFixture) open(ids ...string) {
	fixture.plane.mu.Lock()
	defer fixture.plane.mu.Unlock()
	fixture.plane.matches = append([]string(nil), ids...)
}

// round is one round of the absent close and the edit close after it, as
// absentStrategyClose.run takes it.
func (fixture *closePathsFixture) round() {
	fixture.t.Helper()
	ctx := context.Background()
	fixture.absent.step(ctx)
	fixture.absent.edits.step(ctx, fixture.absent.lastSnapshot)
}

// closes is the body of every close the link received that was not a dry
// run.
func (fixture *closePathsFixture) closes() []string {
	var closes []string
	for _, body := range fixture.plane.requests("/api/v1/alerts/close") {
		if !strings.Contains(body, `"dry_run":true`) {
			closes = append(closes, body)
		}
	}
	return closes
}

// The inactive close of the strategy and its edit and absent close, over
// the same production bundle: inside its effective window nothing asks the
// link to close; outside it, the inactive close closes the alerts of the
// strategy's current version through the link's filter, and neither the
// edit nor the absent close closes the strategy that exists, unedited.
func TestTheClosePathsOfOneStrategyActTogether(t *testing.T) {
	fixture := startClosePathsFixture(t)
	facts := fixture.facts()
	if facts.Revision != 1 {
		t.Fatalf("revision 1 publishes %+v", facts)
	}
	fixture.open(closePathsA1)
	fixture.round()
	if requests := fixture.plane.requests("/api/v1/alerts/close"); len(requests) != 0 {
		t.Fatalf("inside the effective window the link was asked to close %v", requests)
	}
	if counts := fixture.maintenanceCounts(); counts[closeOutcomeDone] != 0 {
		t.Fatalf("the inactive close counted %v inside the effective window", counts)
	}
	fixture.life.clock.Store(fixture.start.Add(30 * time.Minute).UnixMilli())
	fixture.waitFor("the inactive close closes A1", func() bool { return len(fixture.closes()) > 0 },
		func() string { return "no close" })
	fixture.round()
	// Every close is the inactive one, under the version: an edit or absent
	// close of the strategy would name no version or another one.
	wantClosesUnderRevision(t, fixture.closes(), 1)
}

// wantClosesUnderRevision fails unless every close the link received is the
// strategy's, of this deployment's source and tenant, under revision.
func wantClosesUnderRevision(t *testing.T, closes []string, revision int64) {
	t.Helper()
	if len(closes) == 0 {
		t.Fatal("no close was sent")
	}
	for _, body := range closes {
		for _, field := range []string{`"bk_tenant_id":"` + closePathsTenant + `"`, `"event_source_id":"built_in_bk"`,
			`"labels.strategy_id":"` + lifecycleStrategyID + `"`, `"labels.strategy_version":"` + strconv.FormatInt(revision, 10) + `"`} {
			if !strings.Contains(body, field) {
				t.Fatalf("close %s does not carry %s: want the strategy's alerts closed under revision %d", body, field, revision)
			}
		}
	}
}

// maintenanceCounts is the inactive close's outcomes so far, as its lines
// count them (effectiveMaintenance.observeTrace).
func (fixture *closePathsFixture) maintenanceCounts() map[string]uint64 {
	life := fixture.life
	life.observationsMu.Lock()
	defer life.observationsMu.Unlock()
	counts := map[string]uint64{}
	for _, observation := range life.observations {
		if observation.Stage == observability.StageEffectiveTimeMaintenance {
			counts[string(observation.ReasonCode)] += uint64(max(observation.Counts.Events, 1))
		}
	}
	return counts
}

// waitFor polls until done holds, failing with what describe says at the
// test's deadline.
func (fixture *closePathsFixture) waitFor(what string, done func() bool, describe func() string) {
	fixture.t.Helper()
	for deadline := testwait.Deadline(fixture.t); !done(); time.Sleep(20 * time.Millisecond) { // fixed-wait: the poll interval of a condition wait bounded by the test deadline
		if time.Now().After(deadline) {
			fixture.t.Fatalf("%s by the test's deadline: %s", what, describe())
		}
	}
}

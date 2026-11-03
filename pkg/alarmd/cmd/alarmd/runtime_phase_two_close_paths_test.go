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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The close paths of one strategy, together, through the production bundle:
// the inactive close (effective-time maintenance, running on the bundle's
// own loop over the bundle's open alert copy), the edit close and the
// absent close (built by their production constructors over the bundle's
// own control plane), all against one HTTP fake of the link's admin module
// and one real redis-server. The fake answers the bundle's copy as the link
// would; it answers the roster with the strategy only to the edit and
// absent close built here (Basic user closePathsLoopUser), so the bundle's
// own absent loop - whose round runs on a five-minute wall clock - finds
// nothing to act on and every round the case counts is one it ran.

const (
	closePathsLoopUser = "close-paths-loop"
	closePathsSource   = "source"
	closePathsTenant   = "tenant-a"
	closePathsBusiness = int64(2)
	// closePathsWarmUp is how long the fake holds each of the warm-up
	// answers that give the loops' client its latency samples
	// (startClosePathsFixture).
	closePathsWarmUp = 2 * time.Second
)

// openalertsLatencySamples is how many requests the client's latency tail is
// read from (openalerts consoleLatencySamples).
const openalertsLatencySamples = 64

// The alerts of the strategy, by fingerprint (which is also the alert id:
// the source is keyed by the alert id this process sends).
var (
	closePathsA1 = strings.Repeat("a1", 16)
	closePathsA2 = strings.Repeat("a2", 16)
	closePathsK1 = strings.Repeat("b1", 16)
	closePathsU1 = strings.Repeat("c1", 16)
	closePathsU2 = strings.Repeat("c2", 16)
	closePathsN1 = strings.Repeat("d1", 16)
	closePathsT1 = strings.Repeat("e1", 16)
)

// closePathsAlert is one alert's record as the link stores it: the labels,
// dimensions and extra_data of the event that opened it.
type closePathsAlert struct {
	revision   int64
	family     string
	severity   string
	dimensions map[string]any
	facts      *contract.StrategyFactsV2
}

// closePathsRow is one row of the link's reconcile answer; listed says the
// row carries its active alert.
type closePathsRow struct {
	fingerprint, status string
	listed              bool
}

// closePathsConsole is the link's admin module: its one target on the
// case's redis-server, its event source keyed by the alert id, the
// strategy's reconcile answer and its alert records.
type closePathsConsole struct {
	mu       sync.Mutex
	target   openalerts.TargetBinding
	now      func() time.Time
	alerts   map[string]closePathsAlert
	rows     []closePathsRow
	complete bool
	failing  bool
	// reads is every record read, by Basic user; failed counts the reconcile
	// answers refused to the bundle's own copy.
	reads  map[string][]string
	failed int
}

func (console *closePathsConsole) set(complete bool, rows []closePathsRow, alerts map[string]closePathsAlert) {
	console.mu.Lock()
	defer console.mu.Unlock()
	console.complete, console.rows, console.alerts = complete, rows, alerts
}

func (console *closePathsConsole) fail(failing bool) {
	console.mu.Lock()
	defer console.mu.Unlock()
	console.failing, console.failed = failing, 0
}

func (console *closePathsConsole) refusedToCopy() int {
	console.mu.Lock()
	defer console.mu.Unlock()
	return console.failed
}

func (console *closePathsConsole) recordReads() map[string][]string {
	console.mu.Lock()
	defer console.mu.Unlock()
	reads := make(map[string][]string, len(console.reads))
	for user, ids := range console.reads {
		reads[user] = append([]string(nil), ids...)
	}
	return reads
}

func (console *closePathsConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, _, _ := r.BasicAuth()
	console.mu.Lock()
	defer console.mu.Unlock()
	root := testLinkdAPIRoot
	key := console.target.KeyPrefix + ":" + closePathsTenant + ":" + lifecycleStrategyID
	switch path := r.URL.Path; {
	case path == root+"/strategy-index/targets":
		_ = json.NewEncoder(w).Encode([]openalerts.TargetBinding{console.target})
	case path == root+"/event-sources/"+closePathsSource:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": closePathsSource, "revision": 1, "published": 1,
			"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}})
	case path == root+"/strategy-index/browse":
		rows := []any{}
		if user == closePathsLoopUser {
			rows = append(rows, map[string]any{"tenantId": closePathsTenant, "strategyId": lifecycleStrategyID, "key": key, "members": len(console.rows)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"target": console.target, "nextCursor": nil, "phase": "sets",
			"health": map[string]any{"lastSuccess": console.now().UTC().Format(time.RFC3339Nano), "pendingCount": 0}, "rows": rows})
	case path == root+"/strategy-index/reconcile":
		q := r.URL.Query()
		if q.Get("bk_tenant_id") != closePathsTenant || q.Get("strategy_id") != lifecycleStrategyID {
			http.Error(w, "unknown strategy", http.StatusServiceUnavailable)
			return
		}
		if console.failing {
			if user != closePathsLoopUser {
				console.failed++
			}
			http.Error(w, "reconcile unavailable", http.StatusServiceUnavailable)
			return
		}
		rows := make([]any, 0, len(console.rows))
		for _, row := range console.rows {
			alerts := []openalerts.Alert{}
			if row.listed {
				alerts = append(alerts, openalerts.Alert{AlertID: row.fingerprint, EventSourceID: closePathsSource,
					Fingerprint: row.fingerprint, Severity: console.alerts[row.fingerprint].severity})
			}
			rows = append(rows, map[string]any{"fingerprint": row.fingerprint, "status": row.status, "alerts": alerts})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"target": console.target, "tenantId": closePathsTenant,
			"strategyId": lifecycleStrategyID, "key": key, "complete": console.complete,
			"redis": map[string]any{"complete": console.complete}, "alerts": map[string]any{"complete": true}, "rows": rows})
	case path == root+"/"+openalerts.EntityAlerts && user == closePathsLoopUser:
		// The warm-up: held, so the client times a slow answer, and outside
		// the lock, so the answers are held together.
		console.mu.Unlock()
		time.Sleep(closePathsWarmUp) // fixed-wait: the answer time the warm-up exists to give the client's latency samples
		console.mu.Lock()
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	case strings.HasPrefix(path, root+"/alerts/"):
		id := strings.TrimPrefix(path, root+"/alerts/")
		if console.reads == nil {
			console.reads = map[string][]string{}
		}
		console.reads[user] = append(console.reads[user], id)
		alert, ok := console.alerts[id]
		if !ok || r.URL.Query().Get("bk_tenant_id") != closePathsTenant {
			http.Error(w, "no such alert", http.StatusNotFound)
			return
		}
		extra := map[string]any{"evaluation_family": alert.family}
		if alert.facts != nil {
			extra["strategy_facts"] = alert.facts
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tenantId": closePathsTenant, "id": id, "payload": map[string]any{
			"alert_id": id, "bk_tenant_id": closePathsTenant, "event_source_id": closePathsSource, "fingerprint": id,
			"status": "active", "severity": alert.severity, "dimensions": alert.dimensions, "extra_data": extra,
			"labels": map[string]any{"strategy_id": lifecycleStrategyID, "bk_biz_id": closePathsBusiness, "strategy_version": alert.revision}}})
	default:
		http.Error(w, "no route", http.StatusNotFound)
	}
}

// closePathsFixture is the production bundle over the strategy, the fake
// link, and the edit and absent close built over the bundle's control plane.
type closePathsFixture struct {
	t       *testing.T
	life    *lifecycleFixture
	console *closePathsConsole
	control *controlplane.SourceReconciler
	absent  *absentStrategyClose
	copy    *openalerts.Cache
	// link is the client of the edit and absent close built here.
	link *openalerts.HTTPReconciler
	// start is the time the strategy's effective window is laid around: in
	// effect from ten minutes before it to twenty minutes after.
	start time.Time
}

func closePathsKey() openalerts.StrategyKey {
	return openalerts.StrategyKey{TenantID: closePathsTenant, StrategyID: lifecycleStrategyID}
}

// startClosePathsFixture installs the strategy at revision 1, grouped by
// the host and device, without no-data, and opens the bundle.
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
	fixture := &closePathsFixture{t: t, life: life, start: time.Unix(base, 0).UTC(), console: &closePathsConsole{now: now,
		target: openalerts.TargetBinding{EventSourceID: closePathsSource, HookName: "active", KeyPrefix: lifecycleLinkPrefix,
			Address: address, Database: 0, Sources: []string{closePathsSource}}}}

	uqServer := httptest.NewServer(http.HandlerFunc(life.answerQuery))
	t.Cleanup(uqServer.Close)
	consoleServer := httptest.NewServer(fixture.console)
	t.Cleanup(consoleServer.Close)
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
	cfg.PhaseTwo.Linkd.ConsoleURL = consoleServer.URL + testLinkdAPIRoot
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
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
	// The client the bundle builds for these loops (newLinkdIndex), with the
	// loops' own Basic user so the fake can tell them apart. Warmed before
	// the bundle opens, so the steps run on the bundle's timing as before.
	link, err := openalerts.NewHTTPReconciler(openalerts.HTTPReconcilerOptions{BaseURL: cfg.PhaseTwo.Linkd.ConsoleURL,
		Username: closePathsLoopUser, Password: "secret", Client: &http.Client{}, MaxResponseBytes: 1 << 22, Now: now,
		Index:             openalerts.IndexLocation{KeyPrefix: lifecycleLinkPrefix, Address: address, Database: 0},
		LocationConfirmed: true, KeyingEvery: config.LinkdCalibrationInterval, OutputTopic: cfg.Kafka.TriggerEvent.Topic})
	if err != nil {
		t.Fatal(err)
	}
	fixture.link = link
	// The client bounds each request by four times the p90 of its latest
	// requests (openalerts.ConsoleLatency.Deadline). The fake answers on
	// loopback in well under a millisecond, which makes that a deadline of a
	// few milliseconds: a request a loaded machine schedules late is cut,
	// and the round counts evidence_unavailable or record_unreadable for a
	// link that answered. Every sample the p90 is read from is first an
	// answer the fake holds for closePathsWarmUp, so the deadline is
	// seconds; the rounds' own requests replace too few of them to bring it
	// down, which round asserts before each round.
	var warm sync.WaitGroup
	warmErrs := make(chan error, openalertsLatencySamples)
	for range openalertsLatencySamples {
		warm.Add(1)
		go func() {
			defer warm.Done()
			if _, err := link.Entities(testwait.Context(t), openalerts.EntityAlerts, nil); err != nil {
				warmErrs <- err
			}
		}()
	}
	warm.Wait()
	close(warmErrs)
	for err := range warmErrs {
		t.Fatalf("a warm-up read failed: %v", err)
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
	port, ok := life.bundle.workerPorts.OpenAlerts.(*openAlertCopyPort)
	if !ok {
		t.Fatalf("the open alert port is %T", life.bundle.workerPorts.OpenAlerts)
	}
	fixture.copy = port.cache
	// As the bundle wires them (runtime_phase_two_bundle.go), writing to the
	// sink under the bundle's output.
	fixture.absent = newAbsentStrategyClose(life.bundle, control, link, life.sink)
	fixture.absent.edits = newEditStrategyClose(life.bundle, control, link, life.sink)
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

// setMembers makes the strategy's open alert set hold exactly members.
func (fixture *closePathsFixture) setMembers(members ...string) {
	fixture.t.Helper()
	ctx := context.Background()
	key := lifecycleLinkPrefix + ":" + closePathsTenant + ":" + lifecycleStrategyID
	if err := fixture.life.redis.Del(ctx, key).Err(); err != nil {
		fixture.t.Fatal(err)
	}
	if len(members) == 0 {
		return
	}
	values := make([]any, 0, len(members))
	for _, member := range members {
		values = append(values, member)
	}
	if err := fixture.life.redis.SAdd(ctx, key, values...).Err(); err != nil {
		fixture.t.Fatal(err)
	}
}

// round is one round of the absent close and the edit close after it, as
// absentStrategyClose.run takes it.
func (fixture *closePathsFixture) round() {
	fixture.t.Helper()
	if deadline := fixture.link.Latency().Deadline(editCloseRoundTime); deadline < closePathsWarmUp {
		fixture.t.Fatalf("the loops' client bounds a request by %s, under the warm-up's %s: the rounds' requests outnumber the warm-up, "+
			"and a loaded machine can cut a request the fake answered", deadline, closePathsWarmUp)
	}
	ctx := context.Background()
	fixture.absent.step(ctx)
	fixture.absent.edits.step(ctx, fixture.absent.lastRoster)
}

// closes is every close written to the sink so far.
func (fixture *closePathsFixture) closes() []linkdoutput.CloseRequest {
	sink := fixture.life.sink
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]linkdoutput.CloseRequest(nil), sink.closes...)
}

// byReason splits closes by their reason, the inactive close's empty reason
// under linkdoutput.CloseReasonInactive.
func byReason(closes []linkdoutput.CloseRequest) map[string][]linkdoutput.CloseRequest {
	split := map[string][]linkdoutput.CloseRequest{}
	for _, request := range closes {
		reason := request.Reason
		if reason == "" {
			reason = linkdoutput.CloseReasonInactive
		}
		split[reason] = append(split[reason], request)
	}
	return split
}

func fingerprintsOf(closes []linkdoutput.CloseRequest) []string {
	fingerprints := make([]string, 0, len(closes))
	for _, request := range closes {
		fingerprints = append(fingerprints, request.Fingerprint)
	}
	sort.Strings(fingerprints)
	return fingerprints
}

func sortedStrings(values ...string) []string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return sorted
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// editDelta is how much each edit close outcome moved since before.
func (fixture *closePathsFixture) editDelta(before map[string]uint64) map[string]uint64 {
	delta := map[string]uint64{}
	for outcome, count := range fixture.absent.edits.Stats() {
		if count != before[outcome] {
			delta[outcome] = count - before[outcome]
		}
	}
	return delta
}

func wantDelta(t *testing.T, step string, got, want map[string]uint64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: edit close outcomes moved by %v, want exactly %v", step, got, want)
	}
	for outcome, n := range want {
		if got[outcome] != n {
			t.Fatalf("%s: edit close outcomes moved by %v, want exactly %v", step, got, want)
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

// listed is what the bundle's copy lists of the strategy's alerts.
func (fixture *closePathsFixture) listed() ([]string, bool) {
	alerts, listed := fixture.copy.ListActiveAlerts(closePathsKey())
	fingerprints := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		fingerprints = append(fingerprints, alert.Fingerprint)
	}
	sort.Strings(fingerprints)
	return fingerprints, listed
}

func thresholdDimensions(device bool) map[string]any {
	dimensions := map[string]any{"bk_target_ip": lifecycleHostA, "bk_target_cloud_id": "0"}
	if device {
		dimensions["device"] = "eth0"
	}
	return dimensions
}

func eventFacts(plan controlplane.StrategyPlanFacts, noData bool) *contract.StrategyFactsV2 {
	facts := plan.StrategyFacts.ForEvent(noData)
	return &facts
}

// One strategy, edited and cut, then out of its effective time, then
// unlistable, then edited in its conditions only: each close path does its
// part on the same alerts and none decides from what it was not given.
func TestTheClosePathsOfOneStrategyActTogether(t *testing.T) {
	fixture := startClosePathsFixture(t)
	// Each edit is activated in a second of its own. The activation boundary
	// is the Leader's clock in seconds, and two activations in one second
	// are a same-boundary cutover: the cutover leaves the
	// Query Group the edit moves the strategy out of current and draining
	// at once, its timeline open with the previous revision, and its
	// inactive close can close by that revision. That is a case of its own
	// (TestASameBoundaryCutoverLeavesTheOldQueryGroupDrainingNotCurrent),
	// not one this case means to run into.
	nextSecond := func() { fixture.life.clock.Store(fixture.life.clock.Load() + time.Second.Milliseconds()) }
	key := closePathsKey()

	before := fixture.facts()
	if before.Revision != 1 || before.StrategyFacts == nil || !sameStrings(before.DimensionFields, sortedStrings("bk_target_cloud_id", "bk_target_ip", "device")) {
		t.Fatalf("revision 1 publishes %+v, want the host and device grouping with strategy facts", before)
	}
	// Raised under revision 1: A1 and A2 carry the device dimension. K1 was
	// raised under revision 2. U1 and U2 are rows the link cannot confirm:
	// its answer is cut. Each has a record; U1's would decide
	// dimensions_changed if anyone read it.
	alerts := map[string]closePathsAlert{
		closePathsA1: {revision: 1, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(true), facts: eventFacts(before, false)},
		closePathsA2: {revision: 1, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(true), facts: eventFacts(before, false)},
		closePathsK1: {revision: 2, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(false)},
		closePathsU1: {revision: 1, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(true), facts: eventFacts(before, false)},
		closePathsU2: {revision: 1, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(true), facts: eventFacts(before, false)},
	}
	fixture.console.set(false, []closePathsRow{
		{fingerprint: closePathsA1, status: "matched", listed: true},
		{fingerprint: closePathsA2, status: "matched", listed: true},
		{fingerprint: closePathsK1, status: "matched", listed: true},
		{fingerprint: closePathsU1, status: "unknown", listed: true},
		{fingerprint: closePathsU2, status: "unknown"},
	}, alerts)
	fixture.setMembers(closePathsA1, closePathsA2, closePathsK1, closePathsU1)

	// Step 1: the edit removes the device dimension, inside the effective
	// window.
	nextSecond()
	edited := fixture.publish(2, []string{"bk_target_ip", "bk_target_cloud_id"}, false, nil)
	if !sameStrings(edited.DimensionFields, sortedStrings("bk_target_cloud_id", "bk_target_ip")) || edited.StrategyFacts == nil ||
		edited.StrategyFacts.MetricDigest != before.StrategyFacts.MetricDigest || edited.StrategyFacts.DimensionDigest == before.StrategyFacts.DimensionDigest {
		t.Fatalf("revision 2 publishes %+v, want the host grouping, the same metric and another grouping digest", edited)
	}
	confirmed := sortedStrings(closePathsA1, closePathsA2, closePathsK1)
	fixture.waitFor("the copy lists the strategy's alerts", func() bool {
		_, listed := fixture.listed()
		return listed
	}, func() string {
		stats, _ := json.Marshal(fixture.copy.Stats())
		return "copy=" + string(stats)
	})
	if got, _ := fixture.listed(); !sameStrings(got, confirmed) {
		t.Fatalf("the copy lists %v of the cut answer, want exactly the confirmed rows %v", got, confirmed)
	}
	stats := fixture.absent.edits.Stats()
	fixture.round()
	step1 := fixture.closes()
	if changed := byReason(step1)[linkdoutput.CloseReasonStrategyChanged]; len(step1) != 2 || len(changed) != 2 ||
		!sameStrings(fingerprintsOf(changed), sortedStrings(closePathsA1, closePathsA2)) {
		t.Fatalf("step 1 closed %+v, want exactly A1 and A2 for the edit", step1)
	}
	for _, request := range step1 {
		if request.Detail != linkdoutput.StrategyChangedDimensions || request.TenantID != closePathsTenant || request.StrategyID != 1001 ||
			request.StrategyRevision != 2 || request.BusinessID != closePathsBusiness || request.AlertInstanceID != request.Fingerprint {
			t.Fatalf("step 1 close %+v, want dimensions_changed under revision 2 on the alert's own id and business", request)
		}
		if _, err := linkdoutput.ConvertClose(request); err != nil {
			t.Fatalf("step 1 close %+v does not convert: %v", request, err)
		}
	}
	wantDelta(t, "step 1", fixture.editDelta(stats), map[string]uint64{editclose.OutcomeEvidenceTruncated: 1,
		editclose.OutcomeDimensionsChanged: 2, editclose.OutcomeCurrentRevision: 1, editclose.OutcomeCloseSent: 2})
	// Only the confirmed rows' records were read; nobody read the unknown
	// rows' records.
	reads := fixture.console.recordReads()
	if loop := sortedStrings(reads[closePathsLoopUser]...); !sameStrings(loop, confirmed) {
		t.Fatalf("the edit close read the records %v, want exactly the confirmed rows' %v", loop, confirmed)
	}
	for user, ids := range reads {
		for _, id := range ids {
			if id == closePathsU1 || id == closePathsU2 {
				t.Fatalf("%s read the record of %s, a row the link could not confirm", user, id)
			}
		}
	}
	// The index set does not stand in: the reconcile gave usable rows.
	if members, ok := fixture.copy.UnlistedMembers(key); ok {
		t.Fatalf("the index set stands in with %v while the cut answer lists alerts", members)
	}
	// The strategy exists: the absent close decides nothing. Inside its
	// window, the inactive close closes nothing.
	if absent := fixture.absent.Stats(); absent[absentalerts.OutcomeCloseDecided] != 0 || absent[absentalerts.OutcomeCloseSent] != 0 {
		t.Fatalf("the absent close counted %v for a strategy that exists", absent)
	}
	if counts := fixture.maintenanceCounts(); counts[closeOutcomeAcked] != 0 || counts[closeOutcomeIndexSetAcked] != 0 {
		t.Fatalf("the inactive close counted %v inside the effective window", counts)
	}

	// Step 2: the same strategy, now outside its effective window. The
	// inactive close closes every listed alert; the edit close closes A1
	// and A2 again. A1 and A2 then have two closes, one per reason: two
	// events (their ids are salted by the reason) addressing the same alert
	// (the wire's alert_id is the fingerprint the link resolves the close
	// by). The link closes the active alert on the first; the second finds
	// no active alert of that fingerprint and is a no-op. Neither close
	// path holds the other back, and neither reaches an unconfirmed row.
	fixture.life.clock.Store(fixture.start.Add(30 * time.Minute).UnixMilli())
	stats = fixture.absent.edits.Stats()
	fixture.round()
	wantDelta(t, "step 2", fixture.editDelta(stats), map[string]uint64{editclose.OutcomeEvidenceTruncated: 1,
		editclose.OutcomeDimensionsChanged: 2, editclose.OutcomeCurrentRevision: 1, editclose.OutcomeRemembered: 1, editclose.OutcomeCloseSent: 2})
	fixture.waitFor("the inactive close closes the listed alerts", func() bool {
		return fixture.maintenanceCounts()[closeOutcomeAcked] >= 3
	}, func() string { counts, _ := json.Marshal(fixture.maintenanceCounts()); return string(counts) })
	step2 := fixture.closes()[len(step1):]
	split := byReason(step2)
	if len(split) != 2 || !sameStrings(fingerprintsOf(split[linkdoutput.CloseReasonInactive]), confirmed) ||
		!sameStrings(fingerprintsOf(split[linkdoutput.CloseReasonStrategyChanged]), sortedStrings(closePathsA1, closePathsA2)) {
		t.Fatalf("step 2 closed %+v, want the listed alerts as inactive and A1, A2 for the edit", step2)
	}
	events := map[string]map[string]string{}
	for _, request := range step2 {
		if request.TenantID != closePathsTenant || request.StrategyID != 1001 || request.StrategyRevision != 2 || request.BusinessID != closePathsBusiness {
			t.Fatalf("step 2 close %+v, want the strategy's tenant, business and revision 2", request)
		}
		event, err := linkdoutput.ConvertClose(request)
		if err != nil {
			t.Fatalf("step 2 close %+v does not convert: %v", request, err)
		}
		if event.AlertID != request.Fingerprint {
			t.Fatalf("step 2 close of %s addresses alert %s", request.Fingerprint, event.AlertID)
		}
		if events[request.Fingerprint] == nil {
			events[request.Fingerprint] = map[string]string{}
		}
		events[request.Fingerprint][event.EventID] = request.Reason
	}
	for _, fingerprint := range []string{closePathsA1, closePathsA2} {
		if len(events[fingerprint]) != 2 {
			t.Fatalf("A close of %s for each reason is one event each, got %v", fingerprint, events[fingerprint])
		}
	}
	if union := fingerprintsOf(step2); !sameStrings(dedupe(union), confirmed) {
		t.Fatalf("step 2 closed %v, want exactly the listed alerts %v", union, confirmed)
	}
	if counts := fixture.maintenanceCounts(); counts[closeOutcomeIndexSetAcked] != 0 {
		t.Fatalf("the inactive close addressed the index set (%v) while the cut answer lists alerts", counts)
	}

	// Step 3: the reconcile fails, and stays failing past the age a cut
	// answer is acted on. The inactive close falls back to the fresh,
	// trusted index set and closes each member by its fingerprint, U1
	// included: a member stands in for the alert the link would resolve. The
	// edit close does not: it decides from each alert's record, which the
	// set does not give, and counts the strategy evidence_unavailable.
	fixture.console.fail(true)
	fixture.waitFor("the copy is refused a calibration", func() bool {
		fixture.copy.RequestCalibration()
		return fixture.console.refusedToCopy() > 0
	}, func() string { return "no refused reconcile" })
	fixture.life.clock.Store(fixture.life.clock.Load() + (61 * time.Minute).Milliseconds())
	acked := fixture.maintenanceCounts()[closeOutcomeAcked]
	readsBefore := len(fixture.console.recordReads()[closePathsLoopUser])
	stats = fixture.absent.edits.Stats()
	fixture.round()
	wantDelta(t, "step 3", fixture.editDelta(stats), map[string]uint64{editclose.OutcomeEvidenceUnavailable: 1})
	if after := len(fixture.console.recordReads()[closePathsLoopUser]); after != readsBefore {
		t.Fatalf("the edit close read %d records with no listing, want none", after-readsBefore)
	}
	members := sortedStrings(closePathsA1, closePathsA2, closePathsK1, closePathsU1)
	fixture.waitFor("the inactive close closes the index set's members", func() bool {
		return fixture.maintenanceCounts()[closeOutcomeIndexSetAcked] >= 4
	}, func() string {
		counts := fixture.maintenanceCounts()
		return "index_set_close_acked=" + strconv.FormatUint(counts[closeOutcomeIndexSetAcked], 10) +
			" evidence_unavailable=" + strconv.FormatUint(counts[closeOutcomeEvidenceUnavailable], 10)
	})
	step3 := fixture.closes()[len(step1)+len(step2):]
	if split := byReason(step3); len(split) != 1 || !sameStrings(fingerprintsOf(split[linkdoutput.CloseReasonInactive]), members) {
		t.Fatalf("step 3 closed %+v, want each member of the set once, as inactive, and nothing for the edit", step3)
	}
	for _, request := range step3 {
		if request.AlertInstanceID != request.Fingerprint || request.StrategyRevision != 2 || request.BusinessID != closePathsBusiness {
			t.Fatalf("step 3 close %+v, want the member as its own id on the strategy's business and revision 2", request)
		}
		if _, err := linkdoutput.ConvertClose(request); err != nil {
			t.Fatalf("step 3 close %+v does not convert: %v", request, err)
		}
	}
	if counts := fixture.maintenanceCounts(); counts[closeOutcomeIndexSetAcked] != 4 || counts[closeOutcomeAcked] != acked+4 {
		t.Fatalf("the inactive close counted %v, want four index_set_close_acked among four more close_acked", counts)
	}

	// Step 4: the link answers again, whole: the alerts closed above are
	// gone, and the strategy now detects no-data. A no-data alert N1 and a
	// threshold alert T1 were raised under revision 3; revision 4 changes
	// only the filter conditions. The condition rule is held: N1 is counted
	// condition_rule_held and kept, T1 (a threshold alert, not the rule's to
	// judge) is kept, and the edit closes nothing.
	fixture.console.fail(false)
	nextSecond()
	noData := fixture.publish(3, []string{"bk_target_ip", "bk_target_cloud_id"}, true, nil)
	if !noData.NoData || noData.StrategyFacts == nil || noData.StrategyFacts.NoDataDimensionDigest == "" {
		t.Fatalf("revision 3 publishes %+v, want no-data detected with its grouping digest", noData)
	}
	fixture.console.set(true, []closePathsRow{
		{fingerprint: closePathsN1, status: "matched", listed: true},
		{fingerprint: closePathsT1, status: "matched", listed: true},
	}, map[string]closePathsAlert{
		closePathsN1: {revision: 3, family: "no_data", severity: "warning", facts: eventFacts(noData, true),
			dimensions: map[string]any{"bk_target_ip": lifecycleHostA, "bk_target_cloud_id": "0", contract.NoDataDimensionTag: true}},
		closePathsT1: {revision: 3, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(false), facts: eventFacts(noData, false)},
	})
	fixture.setMembers(closePathsN1, closePathsT1)
	nextSecond()
	conditioned := fixture.publish(4, []string{"bk_target_ip", "bk_target_cloud_id"}, true,
		[]any{map[string]any{"key": "bk_target_ip", "method": "eq", "value": []any{lifecycleHostA}}})
	if c, n := conditioned.StrategyFacts, noData.StrategyFacts; c == nil || c.MetricDigest != n.MetricDigest || c.DimensionDigest != n.DimensionDigest ||
		c.NoDataDimensionDigest != n.NoDataDimensionDigest || c.ConditionMD5 == n.ConditionMD5 || c.ConditionBasis != n.ConditionBasis {
		t.Fatalf("revision 4 publishes facts %+v after %+v, want only the condition digest changed", c, n)
	}
	stats = fixture.absent.edits.Stats()
	closed := len(fixture.closes())
	fixture.round()
	wantDelta(t, "step 4", fixture.editDelta(stats), map[string]uint64{editclose.OutcomeConditionRuleHeld: 1, editclose.OutcomeKept: 2})
	if changed := byReason(fixture.closes()[closed:])[linkdoutput.CloseReasonStrategyChanged]; len(changed) != 0 {
		t.Fatalf("the condition-only edit closed %+v, want nothing while the rule is held", changed)
	}
	if absent := fixture.absent.Stats(); absent[absentalerts.OutcomeCloseDecided] != 0 || absent[absentalerts.OutcomeCloseSent] != 0 {
		t.Fatalf("the absent close counted %v for a strategy that exists", absent)
	}
	if absent := byReason(fixture.closes())[linkdoutput.CloseReasonAbsent]; len(absent) != 0 {
		t.Fatalf("the absent close closed %+v for a strategy that exists", absent)
	}
}

func dedupe(sorted []string) []string {
	out := make([]string, 0, len(sorted))
	for i, value := range sorted {
		if i == 0 || value != sorted[i-1] {
			out = append(out, value)
		}
	}
	return out
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/go-redis/redis/v8"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

// overlaySeries is one series the query service answers for the host
// overlay case: the agent it names and its own address and cloud, and the
// host CMDB places it at by that agent.
type overlaySeries struct {
	name    string
	agent   string
	ip      string
	cloud   string
	hostID  int
	hostIP  string
	overlay bool
}

var overlaySeriesSet = []overlaySeries{
	// Placed by its agent at 192.0.2.172 while it reports another address.
	{name: "another address", agent: "agent-live", ip: "198.51.100.9", cloud: "0", hostID: 720002, hostIP: "192.0.2.172", overlay: true},
	// Placed by its agent while it reports no address at all.
	{name: "no address", agent: "agent-bare", ip: "", cloud: "0", hostID: 720004, hostIP: "192.0.2.174", overlay: true},
	// Placed by its agent and reporting its host's own address: nothing is
	// written over it, and its fingerprint is the one it always had.
	{name: "the host's address", agent: "agent-same", ip: "192.0.2.173", cloud: "0", hostID: 720003, hostIP: "192.0.2.173"},
}

// overlayAggregation is the item's aggregation, and so the identity the
// alert is filed by.
var overlayAggregation = []string{"bk_agent_id", "bk_target_cloud_id", "bk_target_ip"}

func (series overlaySeries) rawDimensions() map[string]json.RawMessage {
	encode := func(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	return map[string]json.RawMessage{
		"bk_agent_id": encode(series.agent), "bk_target_cloud_id": encode(series.cloud), "bk_target_ip": encode(series.ip),
	}
}

// pythonDimensions are the record's dimensions as Python's TopoNodeFuller
// leaves them for a host it placed by agent (fullers.py:57-74): the host's
// address, its cloud and its id written over the record's own.
func (series overlaySeries) pythonDimensions() map[string]json.RawMessage {
	dimensions := series.rawDimensions()
	if !series.overlay {
		return dimensions
	}
	address, _ := json.Marshal(series.hostIP)
	dimensions["bk_target_ip"] = address
	dimensions["bk_target_cloud_id"] = json.RawMessage(`"0"`)
	// Written as the string Python's fuller writes, and only because the
	// record carries no bk_host_id key (fullers.py:66-71).
	dimensions["bk_host_id"], _ = json.Marshal(strconv.Itoa(series.hostID))
	return dimensions
}

func overlayDigest(t *testing.T, dimensions map[string]json.RawMessage) string {
	t.Helper()
	names := make([]string, 0, len(dimensions))
	for name := range dimensions {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]contract.DimensionFieldV2, 0, len(names))
	for _, name := range names {
		fields = append(fields, contract.DimensionFieldV2{Name: name, Value: dimensions[name]})
	}
	digest, err := contract.DeriveDimensionIdentityDigestV2("tenant-a", "2", fields)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// overlayHostRecords are the three hosts in CMDB, each under its address and
// its id, each carrying the agent the agent hash maps to it.
func overlayHostRecords() map[string]string {
	records := map[string]string{}
	for _, series := range overlaySeriesSet {
		id := strconv.Itoa(series.hostID)
		record := `{"bk_host_id":` + id + `,"bk_host_innerip":"` + series.hostIP + `","bk_cloud_id":0,"bk_biz_id":2,` +
			`"bk_agent_id":"` + series.agent + `"}`
		records[series.hostIP+"|0"] = record
		records[id] = record
	}
	return records
}

// answerOverlaySeries is the query service for the case: the three series at
// the window's last second, at the fixture's value.
func answerOverlaySeries(fixture *lifecycleFixture, value *atomic.Int64) func(http.ResponseWriter, *http.Request) bool {
	return func(writer http.ResponseWriter, request *http.Request) bool {
		writer.Header().Set("Content-Type", "application/json")
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil || end <= 0 {
			end = fixture.clock.Load() / 1000
		}
		if end > 1_000_000_000_000 {
			end /= 1000
		}
		series := make([]string, 0, len(overlaySeriesSet))
		for _, one := range overlaySeriesSet {
			series = append(series, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
				`"group_keys":["bk_agent_id","bk_target_cloud_id","bk_target_ip"],`+
				`"group_values":["`+one.agent+`","`+one.cloud+`","`+one.ip+`"],`+
				`"values":[[`+strconv.FormatInt((end-1)*1000, 10)+`,`+strconv.FormatInt(value.Load(), 10)+`]]}`)
		}
		_, _ = writer.Write([]byte(`{"series":[` + strings.Join(series, ",") + `],"status":null,"trace_id":"host-overlay",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
		return true
	}
}

// startOverlayFixture is the lifecycle fixture on the given protocol with the
// overlay case's hosts and agent hash in CMDB and its strategy installed:
// the threshold item aggregated by agent, address and cloud, no target, no
// no-data section. The series report the returned value, over the threshold
// until a case lowers it.
func startOverlayFixture(t *testing.T, protocol string) (*lifecycleFixture, *atomic.Int64) {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	return startOverlayFixtureOn(t, address, client, protocol)
}

// startOverlayFixtureOn is startOverlayFixture on a redis-server the case
// started, for a case that writes what the bundle reads at its start.
func startOverlayFixtureOn(t *testing.T, address string, client *redis.Client, protocol string) (*lifecycleFixture, *atomic.Int64) {
	t.Helper()
	ctx := context.Background()
	prefix := validGoAccessRuntimeConfig().PlatformKeyPrefix()
	for _, agents := range []string{prefix + ".cache.cmdb.agent_id", "tenant-a." + prefix + ".cache.cmdb.agent_id"} {
		for _, series := range overlaySeriesSet {
			if err := client.HSet(ctx, agents, series.agent, strconv.Itoa(series.hostID)).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The legacy protocol's strategy carries no snapshot revision, as the
	// platform's legacy document does not.
	strategy := lifecycleStrategy{revision: 7, unrevisioned: protocol == config.OutputProtocolLegacy, noDataOff: true, edit: func(item map[string]any) {
		dimensions := make([]any, 0, len(overlayAggregation))
		for _, name := range overlayAggregation {
			dimensions = append(dimensions, name)
		}
		item["query_configs"].([]any)[0].(map[string]any)["agg_dimension"] = dimensions
	}}
	fixture := startLifecycleFixtureOn(t, address, client, strategy, overlayHostRecords(), func(cfg *config.Config) {
		cfg.PhaseTwo.Output.Protocol = protocol
	})
	value := &atomic.Int64{}
	value.Store(90)
	fixture.interceptQueries(answerOverlaySeries(fixture, value))
	return fixture, value
}

// overlayThresholdEvents runs round 1 and returns its threshold ABNORMAL
// events by the agent each names.
func overlayThresholdEvents(t *testing.T, fixture *lifecycleFixture) map[string]contract.TriggerEventV1 {
	t.Helper()
	fixture.mustAttempt(1)
	fixture.sink.mu.Lock()
	written := append([]contract.TriggerEventV1(nil), fixture.sink.written...)
	fixture.sink.mu.Unlock()
	byAgent := map[string]contract.TriggerEventV1{}
	for _, event := range written {
		if noDataTagged(event) || event.EventKind != contract.TriggerEventAbnormal {
			continue
		}
		var agent string
		if err := json.Unmarshal(event.RecordRef.Dimensions["bk_agent_id"], &agent); err != nil {
			t.Fatalf("event %s names no agent: %v", event.EventID, err)
		}
		if _, seen := byAgent[agent]; seen {
			t.Fatalf("two ABNORMAL events for agent %s in round 1", agent)
		}
		byAgent[agent] = event
	}
	if len(byAgent) != len(overlaySeriesSet) {
		t.Fatalf("round 1 wrote ABNORMAL events for %d agents (%d events in all), want %d", len(byAgent), len(written), len(overlaySeriesSet))
	}
	return byAgent
}

// sameDimensions reports whether two dimension sets hold the same names with
// byte-identical values.
func sameDimensions(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		other, ok := right[name]
		if !ok || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

// A series CMDB places by its agent at a host whose address is not the one
// the series reports - another address, or none - alerts under the
// fingerprint Python gives the record its fuller rewrote with CMDB's address,
// cloud and id (fullers.py:57-74), through the production bundle from the
// query answer and the CMDB cache in Redis to the decision written; its
// series identity stays the record's own. A series that reports its host's
// own address carries nothing over it, and its fingerprint is byte for byte
// the one its own dimensions give.
//
// On the native protocol that is the decision's DedupeMD5 and the alert_id
// the consumer's message carries; on the legacy protocol it is the dedupe
// md5 the converter files the monitor event under.
func TestAHostPlacedByAgentAlertsUnderPythonsFingerprintFromCMDBsAddress(t *testing.T) {
	identity := contract.MonitorOutputIdentity{DimensionFields: overlayAggregation}
	expect := func(t *testing.T, series overlaySeries) (want, own string) {
		t.Helper()
		var err error
		if want, err = contract.MonitorDedupeMD5(lifecycleStrategyID, "2", series.pythonDimensions(), identity); err != nil {
			t.Fatal(err)
		}
		if own, err = contract.MonitorDedupeMD5(lifecycleStrategyID, "2", series.rawDimensions(), identity); err != nil {
			t.Fatal(err)
		}
		if series.overlay == (want == own) {
			t.Fatalf("%s: the fixture's own fingerprint %s and Python's %s do not differ as the case needs", series.name, own, want)
		}
		return want, own
	}
	checkRecord := func(t *testing.T, fixture *lifecycleFixture, series overlaySeries, event contract.TriggerEventV1) {
		t.Helper()
		if !sameDimensions(event.RecordRef.Dimensions, series.pythonDimensions()) {
			t.Errorf("%s: the alert reads dimensions %s, want %s", series.name, mustJSON(t, event.RecordRef.Dimensions), mustJSON(t, series.pythonDimensions()))
		}
		raw := overlayDigest(t, series.rawDimensions())
		if event.RecordRef.DimensionIdentityDigest != raw {
			t.Errorf("%s: the series identity is %s, want the record's own %s", series.name, event.RecordRef.DimensionIdentityDigest, raw)
		}
		// The State key names the series by a digest of its identity
		// (state.RuntimeStateKeyV3): the record's own, never the overlaid.
		rawSegment := stateSeriesSegment(t, raw)
		overlaid := stateSeriesSegment(t, overlayDigest(t, series.pythonDimensions()))
		keptRaw := false
		for _, key := range fixture.runtimeStateKeys() {
			keptRaw = keptRaw || strings.HasSuffix(key, ":"+rawSegment)
			if series.overlay && strings.HasSuffix(key, ":"+overlaid) {
				t.Errorf("%s: State is kept under the overlaid dimensions' identity: %s", series.name, key)
			}
		}
		if !keptRaw {
			t.Errorf("%s: no State key names the record's own identity %s; keys %v", series.name, raw, fixture.runtimeStateKeys())
		}
	}

	t.Run("native", func(t *testing.T) {
		fixture, _ := startOverlayFixture(t, config.OutputProtocolNative)
		events := overlayThresholdEvents(t, fixture)
		for _, series := range overlaySeriesSet {
			event := events[series.agent]
			want, own := expect(t, series)
			if event.DedupeMD5 != want {
				t.Errorf("%s: the alert is filed under %s, want Python's %s (its own dimensions give %s)", series.name, event.DedupeMD5, want, own)
			}
			if got := wireAlertID(t, event); got != want {
				t.Errorf("%s: the consumer's message carries alert %s, want Python's %s", series.name, got, want)
			}
			checkRecord(t, fixture, series, event)
		}
	})

	t.Run("legacy", func(t *testing.T) {
		fixture, _ := startOverlayFixture(t, config.OutputProtocolLegacy)
		events := overlayThresholdEvents(t, fixture)
		converter := legacyoutput.Converter{Store: discardingSnapshotStore{}, PluginID: "alarmd", Now: time.Now}
		for _, series := range overlaySeriesSet {
			event := events[series.agent]
			want, own := expect(t, series)
			converted, err := converter.ConvertBatch(context.Background(), []contract.TriggerEventV1{event})
			if err != nil || len(converted) != 1 {
				t.Fatalf("%s: converting the decision: %d events, %v", series.name, len(converted), err)
			}
			if converted[0].DedupeMD5 != want {
				t.Errorf("%s: the monitor event is filed under %s, want Python's %s (its own dimensions give %s)", series.name, converted[0].DedupeMD5, want, own)
			}
			checkRecord(t, fixture, series, event)
		}
	})
}

// stateSeriesSegment is the series segment of a runtime State key, as
// state.RuntimeStateKeyV3 derives it from the series identity digest.
func stateSeriesSegment(t *testing.T, digest string) string {
	t.Helper()
	derived, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-series-v2", execution.SeriesIdentityDigest(digest))
	if err != nil {
		t.Fatal(err)
	}
	return derived[:32]
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// An alert a series opened under the fingerprint of its own address, before
// its alert identity read CMDB's, is closed as a fact - reason
// superseded_identity, not a recovery - through the production bundle: the
// access path observes the old fingerprint of every admitted series whose
// host overlay moves it, and the fact-close closes it under its own rules
// (two Slots, the alert's own instance id, once). The series is abnormal
// throughout and its alert under CMDB's address opens as usual. Nothing is
// closed for a series whose old alert the consumer does not hold, for a
// series whose overlay leaves its fingerprint as it was, nor for the alert
// under the new fingerprint; and a later round does not write the close
// again while the set still holds it.
//
// The consumer's alerts are there before the first round, so the copy
// calibrates with them when the strategy is first tracked; the copy's
// periodic calibration is what lists an alert opened later.
func TestAnAlertOpenUnderTheRecordsOwnFingerprintIsClosedOnceTheOverlayApplies(t *testing.T) {
	identity := contract.MonitorOutputIdentity{DimensionFields: overlayAggregation}
	fingerprint := func(dimensions map[string]json.RawMessage) string {
		t.Helper()
		value, err := contract.MonitorDedupeMD5(lifecycleStrategyID, "2", dimensions, identity)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	moved, bare, same := overlaySeriesSet[0], overlaySeriesSet[1], overlaySeriesSet[2]
	old, current := fingerprint(moved.rawDimensions()), fingerprint(moved.pythonDimensions())
	unmoved, bareOld := fingerprint(same.rawDimensions()), fingerprint(bare.rawDimensions())
	if old == current || unmoved != fingerprint(same.pythonDimensions()) {
		t.Fatal("the fixture's fingerprints do not move as the case needs")
	}
	fixture, _ := startOverlayFixture(t, config.OutputProtocolNative)
	closer := fixture.bundle.targetScopeClose
	if closer == nil {
		t.Fatal("the fixture's bundle has no fact-close; it is wired with the link's control plane")
	}
	closes := fixture.linkCloses
	step := func(round int64) {
		fixture.mustAttempt(round)
		closer.Step(context.Background())
	}

	// The consumer holds the old alert, the alert under the new fingerprint
	// and the alert of the series whose fingerprint did not move; not the
	// old alert of the series that reports no address.
	for _, held := range []string{old, current, unmoved} {
		fixture.publishOpenAlert(held)
	}
	step(1)
	opened := false
	fixture.sink.mu.Lock()
	for _, event := range fixture.sink.written {
		opened = opened || (event.DedupeMD5 == current && event.EventKind == contract.TriggerEventAbnormal)
	}
	fixture.sink.mu.Unlock()
	if !opened {
		t.Fatalf("round 1 did not open the alert under Python's %s", current)
	}
	// One Slot is at most one observation, and the close waits for a
	// second. Whether round 1 already observed depends on whether the copy
	// read the consumer's set before that round's query ran, so the close
	// comes on round 2's step or round 3's, and by round 3 it is there once.
	if got := closes(); len(got) != 0 {
		t.Fatalf("round 1 closed %+v on at most one observation; the close waits for a second Slot", got)
	}
	fixture.waitOpenAlertSetRead(1)
	step(2)
	step(3)
	got := closes()
	if len(got) != 1 {
		t.Fatalf("round 3 closed %+v, want the old alert's one close; close outcomes %v", got, closer.Stats())
	}
	// The close goes to the link by filter: the strategy's active alert of
	// the old fingerprint, under the superseded decision.
	if !strings.Contains(got[0], `"`+old+`"`) || !strings.Contains(got[0], "superseded_identity") ||
		strings.Contains(got[0], current) || strings.Contains(got[0], unmoved) || strings.Contains(got[0], bareOld) {
		t.Fatalf("round 3 closed %s, want the alert of %s only, under superseded_identity", got[0], old)
	}
	if sent := closer.Stats()[scopeclose.OutcomeSupersededCloseSent]; sent != 1 {
		t.Fatalf("superseded closes sent = %d, want 1", sent)
	}

	// It was decided: the next rounds send nothing more.
	step(4)
	step(5)
	if again := closes(); len(again) != 1 {
		t.Fatalf("the old alert got %d closes by round 5, want the one: %+v", len(again), again)
	}
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// recorded is what the sink has taken so far.
func (sink *recordingPhaseTwoEventSink) recorded() []contract.TriggerEventV1 {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]contract.TriggerEventV1(nil), sink.events...)
}

// noDataContinuous is how many consecutive absent periods the fixture's
// strategy requires before it says anything.
const noDataContinuous = 2

// A strategy with no data reports it, and stops reporting it when the data
// comes back -- under one identity, through the real bundle, against Redis.
//
// This is the question every other no-data test leaves open. The pieces each
// have their own: the group's identity is hashed correctly, the event carries
// the right fields, the tag reaches both protocols. None of them answers
// whether an alert raised this way can ever be closed, and that is the only
// thing that decides if the capability is usable: an alert that opens and
// never closes is worse than one that never opens.
//
// It closes by identity, not by a message. Nothing sends a "clear"; the group
// stops producing anomalies, and the other side pairs the silence with the
// alert through the dimensions md5. So the assertion is that the record the
// recovering round produces is the same object as the one the alerting round
// produced -- same identity digest, and the same anomaly_id up to the period.
// Two objects that differ by anything leave the alert open for ever, and
// nothing anywhere reports it.
func TestNoDataOpensAnAlertAndTheReturningDataClosesIt(t *testing.T) {
	// The native protocol, stated: this is the case where the closing record
	// is a message the consumer pairs by identity. Left to "auto" the
	// fixture's unrevisioned strategy resolved to the compatible protocol,
	// where there is no closing record to send; that case is the next one.
	fixture := startNoDataFixtureOn(t, config.OutputProtocolNative)
	abnormal, recovered := fixture.openAndClose(t)

	if recovered.RecordRef.DimensionIdentityDigest != abnormal.RecordRef.DimensionIdentityDigest {
		t.Fatalf("the closing record is identity %q and the alerting record was %q. Nothing sends a "+
			"clear: the alert closes because the other side pairs the two by identity, so two digests "+
			"leave it open for ever",
			recovered.RecordRef.DimensionIdentityDigest, abnormal.RecordRef.DimensionIdentityDigest)
	}
}

// On the compatible protocol the returning data still decides the closing: the
// RECOVERY is decided and counted on the output line as an event the protocol
// has no message for, and none reaches the sink - that protocol ends the alert
// by the anomalies stopping, which the next case pins.
func TestTheReturningDataDecidesTheClosingOnTheCompatibleProtocolToo(t *testing.T) {
	fixture := startNoDataFixtureOn(t, config.OutputProtocolLegacy)
	ctx := context.Background()
	round := int64(1)
	for ; round <= noDataContinuous+2 && len(fixture.noDataAnomalies()) == 0; round++ {
		fixture.runSlot(ctx, round)
	}
	if len(fixture.noDataAnomalies()) == 0 {
		t.Fatalf("no no-data anomaly after %d absent rounds", noDataContinuous+2)
	}
	fixture.dataReturns()
	recovered := func() bool {
		for _, kind := range fixture.decided() {
			if kind == contract.TriggerEventRecovery {
				return true
			}
		}
		return false
	}
	for next := round + 1; next <= round+3 && !recovered(); next++ {
		fixture.runSlot(ctx, next)
	}
	if !recovered() {
		t.Fatalf("the returning data decided no RECOVERY on the compatible protocol: decided %v", fixture.decided())
	}
	for _, event := range fixture.events.recorded() {
		if event.EventKind == contract.TriggerEventRecovery {
			t.Fatalf("a RECOVERY reached the sink on a protocol that has no message for it: %+v", event.RecordRef)
		}
	}
}

// On the Python-compatible protocol the alert is closed by the anomaly
// stopping, and what keeps it one alert is the anomaly_id.
//
// That protocol carries anomaly points only: there is no closing record to
// send and none is sent, so the backend ends the alert when nothing more
// arrives for it. Two things therefore decide whether a no-data alert raised
// during the shadow period behaves at all, and neither is visible from inside
// alarmd. Every absent round has to produce the same anomaly_id but for its
// period -- an id that moves anywhere else is a new alert every round, and the
// old one never closes because the backend never stops hearing about it. And
// the moment the data returns the anomalies have to stop, because that silence
// is the whole closing mechanism.
//
// The ids come from the real converter rather than being re-derived here. A
// re-derivation is a second statement of the rule and would agree with a wrong
// rule just as readily as with the right one. The record's own identity digest,
// which the native case asserts, says nothing about any of this: a digest built
// from the right fields and an md5 built from the wrong ones look identical
// from inside alarmd, on every round, for ever.
func TestANoDataAlertOnTheCompatibleProtocolIsOneAlertAndThenStops(t *testing.T) {
	fixture := startNoDataFixtureOn(t, config.OutputProtocolLegacy)
	ctx := context.Background()

	var anomalies []contract.TriggerEventV1
	for round := int64(1); round <= noDataContinuous+4 && len(anomalies) < 2; round++ {
		fixture.runSlot(ctx, round)
		anomalies = fixture.noDataAnomalies()
	}
	if len(anomalies) < 2 {
		t.Fatalf("%d no-data anomalies after %d absent rounds with continuous=%d; this needs two "+
			"consecutive ones to say whether they are the same alert",
			len(anomalies), noDataContinuous+4, noDataContinuous)
	}

	// What each id is made of, against what the fixture said rather than
	// against the converter's other answer. Comparing the two rounds to each
	// other says the id is stable, which a wrong id is too; these four
	// segments are the ones a reader of the backend's alert would recognise,
	// and they come from the strategy document, the Plan and the configured
	// no-data level.
	for index, anomaly := range anomalies[:2] {
		id, _ := noDataAnomalyID(t, anomaly)
		want := ".1001.11." + strconv.FormatUint(uint64(noDataConfiguredLevelID), 10)
		if !strings.HasSuffix(id, want) {
			t.Fatalf("anomaly %d has id %q, want it to end %q: strategy, item and level are what the "+
				"backend reads to find which alert this belongs to", index, id, want)
		}
	}

	firstID, firstPeriod := noDataAnomalyID(t, anomalies[0])
	secondID, secondPeriod := noDataAnomalyID(t, anomalies[1])
	if firstPeriod != strconv.FormatInt(anomalies[0].RecordRef.SourceTime, 10) {
		t.Fatalf("the id names period %s and the record's own source time is %d. The period is what "+
			"separates one round's anomaly from the next; taken from anywhere but the record it stops "+
			"describing the round it came from",
			firstPeriod, anomalies[0].RecordRef.SourceTime)
	}
	if firstID != secondID {
		t.Fatalf("anomaly_id without its period moved between two absent rounds: %q then %q. The "+
			"backend pairs rounds into one alert by this; an id that moves opens a new alert every "+
			"round and none of them ever closes", firstID, secondID)
	}
	if firstPeriod == secondPeriod {
		t.Fatalf("both rounds name period %s, so these are not two rounds and the comparison above "+
			"says nothing", firstPeriod)
	}

	// The data returns. Nothing announces it on this protocol -- the alert ends
	// because the anomalies do.
	fixture.dataReturns()
	before := len(fixture.noDataAnomalies())
	for round := int64(1); round <= 3; round++ {
		fixture.runSlot(ctx, int64(len(fixture.events.recorded()))+round+noDataContinuous+4)
	}
	if after := fixture.noDataAnomalies(); len(after) != before {
		t.Fatalf("%d no-data anomalies before the data returned and %d after. On this protocol the "+
			"silence is the close: an anomaly produced for a group that is reporting again keeps the "+
			"alert open for ever", before, len(after))
	}
}

// noDataAnomalies is every no-data anomaly the sink has taken, in order.
func (fixture *noDataFixture) noDataAnomalies() []contract.TriggerEventV1 {
	var anomalies []contract.TriggerEventV1
	for _, event := range fixture.events.recorded() {
		if event.EventKind == contract.TriggerEventAbnormal && noDataTagged(event) {
			anomalies = append(anomalies, event)
		}
	}
	return anomalies
}

// noDataAnomalyID converts one event the way the wire does and splits its
// anomaly_id into the part that identifies the alert and the period. The
// backend's id is "<dimensions md5>.<period>.<strategy>.<item>.<level>", so
// dropping the second segment leaves exactly what has to match.
func noDataAnomalyID(t *testing.T, event contract.TriggerEventV1) (string, string) {
	t.Helper()
	converter := legacyoutput.Converter{
		Store: discardingSnapshotStore{}, PluginID: "alarmd",
		Now: func() time.Time { return time.Unix(event.RecordRef.SourceTime, 0) },
	}
	converted, err := converter.ConvertBatch(context.Background(), []contract.TriggerEventV1{event})
	if err != nil || len(converted) != 1 {
		t.Fatalf("converting the %s record: %d events, %v. A record that cannot be converted never "+
			"reaches the backend at all", event.EventKind, len(converted), err)
	}
	// The record's own anomaly_id, not the trigger's anomaly_ids list. The
	// list is the evidence -- every anomalous point in the decision window --
	// and its first entry moves as the window slides, so reading it would be
	// comparing window starts. The one under anomaly.<level> is this record's
	// identity, and it is the one the backend files the alert under.
	var payload struct {
		ExtraInfo struct {
			OriginAlarm struct {
				Anomaly map[string]struct {
					AnomalyID string `json:"anomaly_id"`
				} `json:"anomaly"`
			} `json:"origin_alarm"`
		} `json:"extra_info"`
	}
	if err := json.Unmarshal(converted[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	level := strconv.FormatUint(uint64(event.PrimaryLevelID), 10)
	id := payload.ExtraInfo.OriginAlarm.Anomaly[level].AnomalyID
	if id == "" {
		t.Fatalf("the %s record carries no anomaly_id at level %s; there is nothing for the backend "+
			"to pair on", event.EventKind, level)
	}
	segments := strings.Split(id, ".")
	if len(segments) != 5 {
		t.Fatalf("anomaly_id %q does not have the backend's five segments", id)
	}
	period := segments[1]
	return strings.Join(append(segments[:1:1], segments[2:]...), "."), period
}

type discardingSnapshotStore struct{}

func (discardingSnapshotStore) SaveBatch(context.Context, []legacyoutput.Snapshot) error { return nil }

func noDataTagged(event contract.TriggerEventV1) bool {
	_, tagged := event.RecordRef.Dimensions[contract.NoDataDimensionTag]
	return tagged
}

const noDataConfiguredLevelID = uint32(1)

type noDataFixture struct {
	t        *testing.T
	base     int64
	clock    *atomic.Int64
	hasData  *atomic.Bool
	runner   phaseTwoQueryGroupRuntime
	events   *recordingPhaseTwoEventSink
	interval int64
	// acked is every event_acked line, for what the rounds decided whether
	// or not their protocol had a message for it.
	ackedMu sync.Mutex
	acked   []observability.Observation
}

// decided is the kinds the rounds decided, as the output lines count them.
func (fixture *noDataFixture) decided() []string {
	fixture.ackedMu.Lock()
	defer fixture.ackedMu.Unlock()
	return decidedEventKinds(fixture.acked)
}

func (fixture *noDataFixture) dataReturns() { fixture.hasData.Store(true) }

// openAndClose runs the absence until it is reported and then lets the data
// come back, returning the record that opened the alert and the one that
// should close it.
//
// Both failures it can raise are the point rather than fixture trouble: a
// strategy that never reports the silence is a capability that does nothing,
// and an absence that produces no closing record leaves the alert standing for
// ever with nothing anywhere reporting it.
func (fixture *noDataFixture) openAndClose(t *testing.T) (contract.TriggerEventV1, contract.TriggerEventV1) {
	t.Helper()
	ctx := context.Background()

	var abnormal *contract.TriggerEventV1
	for round := int64(1); round <= noDataContinuous+2 && abnormal == nil; round++ {
		fixture.runSlot(ctx, round)
		for index := range fixture.events.recorded() {
			event := fixture.events.recorded()[index]
			if event.EventKind == contract.TriggerEventAbnormal && noDataTagged(event) {
				abnormal = &event
				break
			}
		}
	}
	if abnormal == nil {
		t.Fatalf("no no-data anomaly after %d absent rounds with continuous=%d; a strategy that never "+
			"reports the silence is a capability that does nothing", noDataContinuous+2, noDataContinuous)
	}
	if abnormal.PrimaryLevelID != noDataConfiguredLevelID {
		t.Fatalf("the anomaly is at level %d, want the configured no-data level %d: that number is the "+
			"last segment of the anomaly_id and decides which alert it pairs with",
			abnormal.PrimaryLevelID, noDataConfiguredLevelID)
	}

	fixture.dataReturns()
	var recovered *contract.TriggerEventV1
	for round := int64(1); round <= 3 && recovered == nil; round++ {
		fixture.runSlot(ctx, int64(len(fixture.events.recorded()))+int64(round)+noDataContinuous+2)
		for index := range fixture.events.recorded() {
			event := fixture.events.recorded()[index]
			if event.EventKind != contract.TriggerEventAbnormal && noDataTagged(event) {
				recovered = &event
				break
			}
		}
	}
	if recovered == nil {
		t.Fatal("the returning data produced no closing record for the no-data group: the alert " +
			"the absence opened would stay open for ever")
	}
	return *abnormal, *recovered
}

// runSlot moves the clock one period on and runs whatever is due.
func (fixture *noDataFixture) runSlot(ctx context.Context, round int64) {
	fixture.t.Helper()
	fixture.clock.Store((fixture.base + round*fixture.interval + fixture.interval/2) * 1000)
	for attempt := 0; attempt < 4; attempt++ {
		_, attempted, err := fixture.runner.RunOne(ctx)
		if err != nil {
			fixture.t.Fatalf("round %d RunOne error = %v", round, err)
		}
		if !attempted {
			return
		}
	}
}

func startNoDataFixture(t *testing.T) *noDataFixture {
	t.Helper()
	return startNoDataFixtureOn(t, "")
}

// startNoDataFixtureOn is the same fixture on a stated output protocol. The
// two are different questions: on the native protocol the record's own
// identity digest is what pairs an alert with its recovery, and on the
// Python-compatible one it is the anomaly_id the converter builds. A no-data
// alert raised during the shadow period is closed by the second, so both have
// to be exercised and neither stands in for the other.
func startNoDataFixtureOn(t *testing.T, protocol string) *noDataFixture {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	// The native protocol pairs a closing record with its alert by the
	// frozen strategy revision, so its strategy carries one; the compatible
	// protocol's does not need it and is left as the platform writes it.
	var revision int64
	if protocol == config.OutputProtocolNative {
		revision = 7
	}
	installNoDataStrategy(t, ctx, redisClient, revision)

	const interval = int64(60)
	base := time.Now().Unix()
	base += interval - base%interval
	fixture := &noDataFixture{t: t, base: base, clock: &atomic.Int64{}, hasData: &atomic.Bool{}, interval: interval}
	fixture.clock.Store(base * 1000)
	now := func() time.Time { return time.UnixMilli(fixture.clock.Load()) }

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		series := ""
		if fixture.hasData.Load() {
			series = `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
				`"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
				strconv.FormatInt((end-1)*1000, 10) + `,5]]}`
		}
		_, _ = writer.Write([]byte(`{"series":[` + series + `],"status":null,"trace_id":"no-data-round",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-no-data-round"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Output.Protocol = protocol
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	events := &recordingPhaseTwoEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Stage != observability.StageEventACKed {
					return
				}
				fixture.ackedMu.Lock()
				defer fixture.ackedMu.Unlock()
				fixture.acked = append(fixture.acked, observation)
			}),
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	t.Cleanup(func() {
		if shutdownErr := bundle.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("phase-two production Shutdown() error = %v", shutdownErr)
		}
	})
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("Query Groups = %v, want one", bundle.queryGroups)
	}
	queryGroup := bundle.queryGroups[0]
	fixture.runner = settledRunner(bundle, queryGroup)
	fixture.events = events

	// The Plan must actually carry no-data detection, or every round below
	// would pass by judging nothing at all.
	production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	schedule, err := production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
	if err != nil || len(schedule.Plans) != 1 {
		t.Fatalf("initial schedule=%+v error=%v", schedule, err)
	}
	return fixture
}

func installNoDataStrategy(t *testing.T, ctx context.Context, redisClient *redis.Client, revision int64) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["update_time"] = 1725000000
	if revision > 0 {
		document["strategy_revision"] = revision
	}
	// A name and a scenario, because the Python-compatible conversion refuses a
	// strategy without them and the shared document has neither. Nothing
	// noticed while this fixture only ran on the native protocol, where none of
	// it is read -- which is exactly how the compatible path for no-data went
	// unexercised end to end.
	document["name"] = "no data round"
	document["scenario"] = "os"
	item := document["items"].([]any)[0].(map[string]any)
	// The item's name for the same reason: the compatible conversion needs it,
	// and it is what the no-data alert's text names as the metric with nothing
	// arriving.
	item["name"] = "CPU usage"
	item["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	// The whole-item group: no aggregation dimensions, which is the group the
	// backend reports when an item has no data at all. It needs no roster
	// history, so the first absent round already has something to judge.
	item["no_data_config"] = map[string]any{
		"is_enabled": true, "continuous": noDataContinuous,
		"level": noDataConfiguredLevelID, "agg_dimension": []any{},
	}
	for _, detect := range document["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = 1
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":  `[1001]`,
		"alarm-config.strategy_1001": encoded,
	} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

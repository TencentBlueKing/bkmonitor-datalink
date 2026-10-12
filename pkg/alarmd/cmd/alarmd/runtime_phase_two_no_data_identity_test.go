// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"crypto/md5" // The backend's identity hash is MD5; this is a re-statement of it, not a security primitive.
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The identities Python gives the fixture strategy's no-data alerts.
//
// Every literal here was produced by running count_md5 from
// bkmonitor/utils/common_utils.py - the function itself, lifted out of that
// file unchanged - on the value the backend hashes. None was computed by this
// build.
//
// The dimensions md5 is count_md5 of the dict the no-data checker builds: the
// group's dimensions plus __NO_DATA_DIMENSION__: True (alarm_backends/core/
// control/mixins/nodata.py, _process_dimensions and, for the item as a whole,
// the total_no_data_dimensions of the absence check). It is the first segment
// of the anomaly_id and of the record_id on the compatible protocol.
//
// The alert fingerprint is count_md5 of the list Event.cal_dedupe_md5 hashes
// (alarm_backends/core/alert/event.py): strategy_id, target_type, target and
// bk_biz_id, then the value of every tag the adapter keyed the event by
// (MonitorEventAdapter.adapt, adapter.py). The strategy is 1001 of business 2;
// a dimension named host is no target, so the target type is "" and the target
// None. It is the alert_id on the consumer's protocol.
const (
	pythonWholeItemDimensionsMD5  = "3e06a0b6d0560271cafee9f08a6da2d7"
	pythonHostGroupDimensionsMD5  = "9bda727c5fc3c9784ca26c2b48e03df8"
	pythonWholeItemFingerprint    = "49d28928d230ecbac8f2892ff298bf44"
	pythonHostGroupFingerprint    = "bb14ffef696bd0a201c9fb2790597a15"
	pythonHostSeriesFingerprint   = "f7e2839ad7bb20ab266939ef2e1bd561"
	identityFixtureHost           = "192.0.2.10"
	identityFixtureAbnormalValue  = 90
	identityFixtureRecoveredValue = 5
)

// pythonRuleCountMD5 re-states count_md5 (bkmonitor/utils/common_utils.py) for
// the values a no-data identity is built from, independently of this build's
// port: a dict is the sorted list of (str(key), count_md5(value)) pairs; a list
// or tuple is the sorted list of its items' count_md5, hashed as Python's str()
// of that list; anything else is the md5 of str(value). It is used to read the
// dimensions a record carries the way the backend reads them, and to show that
// the literals above decompose by the rule.
func pythonRuleCountMD5(t *testing.T, value any) string {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		pairs := make([]any, 0, len(keys))
		for _, key := range keys {
			pairs = append(pairs, []any{key, pythonRuleCountMD5(t, typed[key])})
		}
		return pythonRuleCountMD5(t, pairs)
	case []any:
		hashes := make([]string, 0, len(typed))
		for _, item := range typed {
			hashes = append(hashes, pythonRuleCountMD5(t, item))
		}
		sort.Strings(hashes)
		// str() of a list of hex strings: single quotes, comma and space.
		text := "[]"
		if len(hashes) > 0 {
			text = "['" + strings.Join(hashes, "', '") + "']"
		}
		return pythonMD5Hex(text)
	case string:
		return pythonMD5Hex(typed)
	case bool:
		if typed {
			return pythonMD5Hex("True")
		}
		return pythonMD5Hex("False")
	case nil:
		return pythonMD5Hex("None")
	case int:
		return pythonMD5Hex(strconv.Itoa(typed))
	}
	t.Fatalf("pythonRuleCountMD5: no rule for %T", value)
	return ""
}

func pythonMD5Hex(text string) string {
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:])
}

// pythonDimensions reads a record's dimensions as the values the backend
// hashes. A JSON string is its text, true is True; nothing else occurs in a
// no-data record.
func pythonDimensions(t *testing.T, dimensions map[string]json.RawMessage) map[string]any {
	t.Helper()
	read := make(map[string]any, len(dimensions))
	for name, raw := range dimensions {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		switch value.(type) {
		case string, bool:
		default:
			t.Fatalf("dimension %s = %s, which no no-data record carries", name, raw)
		}
		read[name] = value
	}
	return read
}

// The literals decompose by the rule: if this fails, the rule re-stated above
// is not count_md5, and nothing it says about a record can be trusted.
func TestThePythonIdentityLiteralsDecomposeByCountMD5(t *testing.T) {
	for name, test := range map[string]struct {
		value any
		want  string
	}{
		"whole-item dimensions": {map[string]any{contract.NoDataDimensionTag: true}, pythonWholeItemDimensionsMD5},
		"host group dimensions": {map[string]any{"host": identityFixtureHost, contract.NoDataDimensionTag: true},
			pythonHostGroupDimensionsMD5},
		"whole-item fingerprint": {[]any{1001, "", nil, 2, true}, pythonWholeItemFingerprint},
		"host group fingerprint": {[]any{1001, "", nil, 2, identityFixtureHost, true}, pythonHostGroupFingerprint},
		"host series fingerprint": {[]any{1001, "", nil, 2, identityFixtureHost},
			pythonHostSeriesFingerprint},
	} {
		if got := pythonRuleCountMD5(t, test.value); got != test.want {
			t.Fatalf("%s: the rule gives %s, the backend's function gave %s", name, got, test.want)
		}
	}
}

// noDataFixtureShape is what an identity case changes about the strategy and
// the data: which no-data dimensions, how many absent periods, whether a
// horizon is configured, and what the item reports while it reports.
type noDataFixtureShape struct {
	continuous   int
	aggDimension []any
	horizon      int64
}

// identityFixture is the no-data fixture with the reported value under the
// case's control.
type identityFixture struct {
	*noDataFixture
	value *atomic.Int64
	round int64
}

// next runs the next Slot, one period after the last, and returns the events
// it produced.
func (fixture *identityFixture) next(t *testing.T) []contract.TriggerEventV1 {
	t.Helper()
	before := len(fixture.events.recorded())
	fixture.round++
	fixture.runSlot(context.Background(), fixture.round)
	return fixture.events.recorded()[before:]
}

// startIdentityFixture is startNoDataFixtureWith for a stated shape: the same
// production bundle, real Redis, strategy document and query fake, with the
// no-data dimensions, the horizon and the reported host and value set by the
// case. The query reports host 192.0.2.10 while hasData is set.
func startIdentityFixture(t *testing.T, protocol string, shape noDataFixtureShape) *identityFixture {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	var revision int64
	if protocol == config.OutputProtocolNative {
		revision = 7
	}
	installShapedNoDataStrategy(t, ctx, redisClient, revision, shape)

	const interval = int64(60)
	base := time.Now().Unix()
	base += interval - base%interval
	fixture := &noDataFixture{t: t, base: base, clock: &atomic.Int64{}, hasData: &atomic.Bool{}, partial: &atomic.Bool{}, interval: interval}
	fixture.clock.Store(base * 1000)
	now := func() time.Time { return time.UnixMilli(fixture.clock.Load()) }
	value := &atomic.Int64{}
	value.Store(identityFixtureAbnormalValue)

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		series := ""
		if fixture.hasData.Load() {
			series = `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
				`"group_keys":["host"],"group_values":["` + identityFixtureHost + `"],"values":[[` +
				strconv.FormatInt((end-1)*1000, 10) + `,` + strconv.FormatInt(value.Load(), 10) + `]]}`
		}
		_, _ = writer.Write([]byte(`{"series":[` + series + `],"status":null,"trace_id":"no-data-identity",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-no-data-identity"
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
	if shape.horizon > 0 {
		horizon := shape.horizon
		cfg.PhaseTwo.NoData.TrackingHorizonSeconds = &horizon
	}

	events := &recordingPhaseTwoEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil }),
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

	// The Plan must carry no-data detection with the configured horizon, or
	// every round below would pass by judging something other than the case.
	production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	schedule, err := production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
	if err != nil || len(schedule.Plans) != 1 {
		t.Fatalf("initial schedule=%+v error=%v", schedule, err)
	}
	// The first Slot is due at base: round zero runs it, and every round
	// after runs the one Slot a period later, so a case's rounds are its
	// Slots one for one.
	return &identityFixture{noDataFixture: fixture, value: value, round: -1}
}

// installShapedNoDataStrategy is installNoDataStrategy with the no-data
// dimensions stated.
func installShapedNoDataStrategy(t *testing.T, ctx context.Context, redisClient *redis.Client, revision int64, shape noDataFixtureShape) {
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
	document["name"] = "no data identity"
	document["scenario"] = "os"
	item := document["items"].([]any)[0].(map[string]any)
	item["name"] = "CPU usage"
	item["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	item["no_data_config"] = map[string]any{
		"is_enabled": true, "continuous": shape.continuous,
		"level": noDataConfiguredLevelID, "agg_dimension": shape.aggDimension,
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

// wireAlertID converts one decision the way the consumer's protocol does and
// returns the alert_id the message carries.
func wireAlertID(t *testing.T, event contract.TriggerEventV1) string {
	t.Helper()
	converter, err := linkdoutput.NewConverter(nil)
	if err != nil {
		t.Fatal(err)
	}
	converted, err := converter.Convert(&event)
	if err != nil {
		t.Fatalf("converting the %s decision: %v", event.EventKind, err)
	}
	var message struct {
		AlertID string `json:"alert_id"`
	}
	if err := json.Unmarshal(converted.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.AlertID != converted.AlertID {
		t.Fatalf("the message says alert %q and the envelope %q", message.AlertID, converted.AlertID)
	}
	return message.AlertID
}

// wantPythonIdentity fails unless the decision is filed under the fingerprint
// and carries the dimensions Python gives the alert.
func wantPythonIdentity(t *testing.T, label string, event contract.TriggerEventV1, fingerprint, dimensionsMD5 string) {
	t.Helper()
	if event.DedupeMD5 != fingerprint {
		t.Fatalf("%s is filed under %q, want Python's %q", label, event.DedupeMD5, fingerprint)
	}
	if got := wireAlertID(t, event); got != fingerprint {
		t.Fatalf("%s reaches the consumer as alert %q, want Python's %q", label, got, fingerprint)
	}
	if got := pythonRuleCountMD5(t, pythonDimensions(t, event.RecordRef.Dimensions)); got != dimensionsMD5 {
		t.Fatalf("%s carries dimensions %v, which hash to %s; want the dimensions Python builds, %s",
			label, event.RecordRef.Dimensions, got, dimensionsMD5)
	}
}

// noDataEventsOf is the no-data decisions among events, in order.
func noDataEventsOf(events []contract.TriggerEventV1) []contract.TriggerEventV1 {
	var tagged []contract.TriggerEventV1
	for _, event := range events {
		if noDataTagged(event) {
			tagged = append(tagged, event)
		}
	}
	return tagged
}

// The record that closes a no-data alert and the records that opened it carry
// the identity Python gives that alert, on the consumer's protocol, through
// the production bundle against Redis (nodata-capability-decomposition,
// section 5.5 R1, R2, R4 and R5, and section 5.10; decision-007 section 3: a
// recovery carries the alert_id of the trigger it closes).
//
// The item has no no-data dimensions, so the group is the item as a whole:
// Python's {__NO_DATA_DIMENSION__: True}. Both records are compared with the
// backend's values rather than with each other: two records filed under the
// same wrong fingerprint still pair with each other and with no alert the
// backend ever opened.
func TestTheWholeItemNoDataAlertOpensAndClosesUnderPythonsIdentity(t *testing.T) {
	fixture := startNoDataFixtureOn(t, config.OutputProtocolNative)
	abnormal, recovered := fixture.openAndClose(t)
	if recovered.EventKind != contract.TriggerEventRecovery {
		t.Fatalf("the closing record is %s, want RECOVERY", recovered.EventKind)
	}
	wantPythonIdentity(t, "the alerting record", abnormal, pythonWholeItemFingerprint, pythonWholeItemDimensionsMD5)
	wantPythonIdentity(t, "the closing record", recovered, pythonWholeItemFingerprint, pythonWholeItemDimensionsMD5)
}

// One host's no-data alert and its threshold alert, from one strategy through
// the production bundle against Redis, are two alerts filed under Python's two
// fingerprints, and each closes under its own (nodata-capability-decomposition,
// section 5.5 R1 and R2; no-data progress P10: the tag is part of the dedupe
// key, so the two are separate alerts).
//
// Round by round: the host reports a value over the threshold, so the
// threshold alert opens and the host's group is remembered; the host stops
// reporting for two rounds, the continuous count, and the no-data alert opens
// on the second; the host comes back under the threshold, and both close.
func TestOneHostsNoDataAndThresholdAlertsOpenAndCloseUnderPythonsTwoIdentities(t *testing.T) {
	fixture := startIdentityFixture(t, config.OutputProtocolNative, noDataFixtureShape{
		continuous: 2, aggDimension: []any{"host"},
	})

	fixture.hasData.Store(true)
	first := fixture.next(t)
	if len(first) != 1 || noDataTagged(first[0]) || first[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1: %d events %v, want the threshold alert alone", len(first), eventKinds(first))
	}
	threshold := first[0]
	if threshold.DedupeMD5 != pythonHostSeriesFingerprint || wireAlertID(t, threshold) != pythonHostSeriesFingerprint {
		t.Fatalf("the threshold alert is filed under %q, want Python's %q", threshold.DedupeMD5, pythonHostSeriesFingerprint)
	}

	fixture.hasData.Store(false)
	if second := fixture.next(t); len(second) != 0 {
		t.Fatalf("round 2: %v, want nothing: one absent period of two", eventKinds(second))
	}
	third := fixture.next(t)
	if len(third) != 1 || !noDataTagged(third[0]) || third[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 3: %v, want the host's no-data alert alone", eventKinds(third))
	}
	wantPythonIdentity(t, "the no-data alert", third[0], pythonHostGroupFingerprint, pythonHostGroupDimensionsMD5)
	if third[0].DedupeMD5 == threshold.DedupeMD5 {
		t.Fatal("the no-data alert and the threshold alert of one host share a fingerprint")
	}

	fixture.value.Store(identityFixtureRecoveredValue)
	fixture.hasData.Store(true)
	fourth := fixture.next(t)
	var closedNoData, closedThreshold *contract.TriggerEventV1
	for index := range fourth {
		event := fourth[index]
		if event.EventKind != contract.TriggerEventRecovery {
			t.Fatalf("round 4: %v, want recoveries only", eventKinds(fourth))
		}
		if noDataTagged(event) {
			closedNoData = &event
		} else {
			closedThreshold = &event
		}
	}
	if closedNoData == nil || closedThreshold == nil || len(fourth) != 2 {
		t.Fatalf("round 4: %v, want one recovery for each alert", eventKinds(fourth))
	}
	wantPythonIdentity(t, "the no-data recovery", *closedNoData, pythonHostGroupFingerprint, pythonHostGroupDimensionsMD5)
	if closedThreshold.DedupeMD5 != pythonHostSeriesFingerprint || wireAlertID(t, *closedThreshold) != pythonHostSeriesFingerprint {
		t.Fatalf("the threshold recovery is filed under %q, want Python's %q", closedThreshold.DedupeMD5, pythonHostSeriesFingerprint)
	}
}

// On the compatible protocol the same host's no-data anomalies carry Python's
// dimensions md5 in their anomaly_id, and the anomalies stop when the host
// comes back, which is how that protocol closes the alert
// (nodata-capability-decomposition, section 5.10: anomaly_id is
// "<dims_md5>.<check_ts>.<strategy>.<item>.<level>", and a returning group
// shows as the anomalies no longer arriving).
func TestOneHostsNoDataAnomaliesCarryPythonsIdentityAndStopWhenItReturns(t *testing.T) {
	fixture := startIdentityFixture(t, config.OutputProtocolLegacy, noDataFixtureShape{
		continuous: 2, aggDimension: []any{"host"},
	})
	fixture.value.Store(identityFixtureRecoveredValue)
	fixture.hasData.Store(true)
	if first := noDataEventsOf(fixture.next(t)); len(first) != 0 {
		t.Fatalf("round 1: %v, want no no-data record while the host reports", eventKinds(first))
	}
	fixture.hasData.Store(false)
	if second := noDataEventsOf(fixture.next(t)); len(second) != 0 {
		t.Fatalf("round 2: %v, want nothing: one absent period of two", eventKinds(second))
	}
	for round := 3; round <= 4; round++ {
		anomalies := noDataEventsOf(fixture.next(t))
		if len(anomalies) != 1 || anomalies[0].EventKind != contract.TriggerEventAbnormal {
			t.Fatalf("round %d: %v, want one no-data anomaly", round, eventKinds(anomalies))
		}
		id, period := noDataAnomalyID(t, anomalies[0])
		want := pythonHostGroupDimensionsMD5 + ".1001.11." + strconv.FormatUint(uint64(noDataConfiguredLevelID), 10)
		if id != want {
			t.Fatalf("round %d: anomaly_id without its period is %q, want %q", round, id, want)
		}
		if period != strconv.FormatInt(anomalies[0].RecordRef.SourceTime, 10) {
			t.Fatalf("round %d: the anomaly_id names period %s, the record's source time is %d",
				round, period, anomalies[0].RecordRef.SourceTime)
		}
	}
	fixture.hasData.Store(true)
	for round := 5; round <= 7; round++ {
		for _, event := range noDataEventsOf(fixture.next(t)) {
			if event.EventKind == contract.TriggerEventAbnormal {
				t.Fatalf("round %d: a no-data anomaly for a host that is reporting again keeps the alert open", round)
			}
		}
	}
}

// An absence whose tracking stopped is never reported again, so an alert the
// consumer closed after the stop is not opened a second time - through the
// production bundle against Redis, with the horizon set where an operator sets
// it (no-data tracking retention proposal, section 1 items 3, 5 and 6, and
// section 3: the whole item keeps a stop mark so the next empty rounds do not
// start its absence again).
//
// The horizon is three periods. The item is silent from the first round, so
// the alert opens on the second (continuous two) and is reported again on the
// third; the fourth is three periods after the absence began and says nothing.
// The consumer then closes the alert on its own, and six more silent rounds
// must send nothing at all: an ABNORMAL among them is the closed alert opened
// again. The data then comes back and goes away again, and the new absence is
// reported, which shows the silence above was the stop and not a fixture that
// had stopped producing anything.
func TestAnAbsenceThatStoppedAtTheHorizonDoesNotReopenAClosedAlert(t *testing.T) {
	fixture := startIdentityFixture(t, config.OutputProtocolNative, noDataFixtureShape{
		continuous: 2, aggDimension: []any{}, horizon: 3 * 60,
	})
	consumer := map[string]string{}
	reopened := 0
	deliver := func(events []contract.TriggerEventV1) {
		for _, event := range events {
			alert := event.DedupeMD5
			switch event.EventKind {
			case contract.TriggerEventAbnormal:
				if consumer[alert] == "closed" {
					reopened++
				}
				consumer[alert] = "open"
			case contract.TriggerEventRecovery:
				consumer[alert] = "resolved"
			}
		}
	}

	wantRound := func(round int, want []string) []contract.TriggerEventV1 {
		t.Helper()
		events := noDataEventsOf(fixture.next(t))
		got := make([]string, 0, len(events))
		for _, event := range events {
			got = append(got, event.EventKind)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("round %d: no-data decisions %v, want %v", round, got, want)
		}
		deliver(events)
		return events
	}
	wantRound(1, nil)
	opened := wantRound(2, []string{contract.TriggerEventAbnormal})
	wantPythonIdentity(t, "the no-data alert", opened[0], pythonWholeItemFingerprint, pythonWholeItemDimensionsMD5)
	wantRound(3, []string{contract.TriggerEventAbnormal})
	wantRound(4, nil)

	// The consumer closes the alert on its own.
	consumer[pythonWholeItemFingerprint] = "closed"
	for round := 5; round <= 10; round++ {
		wantRound(round, nil)
	}
	if reopened != 0 || consumer[pythonWholeItemFingerprint] != "closed" {
		t.Fatalf("after the stop the consumer's alert is %q and was reopened %d times, want it left closed",
			consumer[pythonWholeItemFingerprint], reopened)
	}

	// Data comes back for a round, then the item goes silent again: a new
	// absence, reported on its second round.
	fixture.hasData.Store(true)
	fixture.next(t)
	fixture.hasData.Store(false)
	// The first silent round of the new absence opens nothing. The return
	// above recovered the alert, and a copy with no Console answers from what
	// this process sent, which keeps an alert it recovered answering open for
	// a grace after its RECOVERY: the recovery may go out again here, an
	// orphan at the consumer (at most grace / period of them). What must not
	// come is an ABNORMAL.
	for _, event := range noDataEventsOf(fixture.next(t)) {
		if event.EventKind != contract.TriggerEventRecovery {
			t.Fatalf("round 12: no-data decision %s on the first silent round of a new absence, want none or a repeated RECOVERY",
				event.EventKind)
		}
	}
	again := wantRound(13, []string{contract.TriggerEventAbnormal})
	wantPythonIdentity(t, "the new absence", again[0], pythonWholeItemFingerprint, pythonWholeItemDimensionsMD5)
}

func eventKinds(events []contract.TriggerEventV1) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kind := event.EventKind
		if noDataTagged(event) {
			kind = "no-data " + kind
		}
		kinds = append(kinds, kind)
	}
	return kinds
}

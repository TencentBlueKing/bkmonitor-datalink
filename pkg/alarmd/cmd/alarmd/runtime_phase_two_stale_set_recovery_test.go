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
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// An alert this process opened, held open past the local retention so that a
// complete calibration listing it takes it out of the recent sends, recovers
// after the link's set reads have failed past the stale bound while the
// Console's confirmation stands. The copy is the production one
// (newLinkdIndex, with its own cadence and stale bound), read through the
// worker's port by the evaluator's recovery gate: the series' RECOVERY is
// emitted, and the alert does not stay open at the consumer.
func TestAStaleSetLetsTheRecoveryOfAnAlertThisProcessOpenedGoOut(t *testing.T) {
	linkdAddress, _ := startPhaseTwoRedis(t)
	// The copy's own connection to the link's Redis; closing it is the
	// link's Redis becoming unreachable.
	linkdClient := redis.NewClient(&redis.Options{Addr: linkdAddress, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = linkdClient.Close() })
	clock := &staleSetClock{at: time.Unix(1700000000, 0)}

	plan := staleSetPlan(t)
	port := &openAlertCopyPort{linked: true}
	// The ABNORMAL the evaluator decides for the series, which names the
	// fingerprint the consumer files the alert under.
	opened := staleSetEvaluate(t, plan, port, 300, trigger.DetectionAnomalous, true)
	if opened.TriggerEvent == nil || opened.TriggerEvent.EventKind != contract.TriggerEventAbnormal || opened.TriggerEvent.DedupeMD5 == "" {
		t.Fatalf("setup: the anomalous record decided %+v, want an ABNORMAL with a fingerprint", opened.TriggerEvent)
	}
	fingerprint := opened.TriggerEvent.DedupeMD5
	key := openalerts.StrategyKey{TenantID: opened.TriggerEvent.TenantID, StrategyID: opened.TriggerEvent.PlanRef.StrategyID}

	target := openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "hook:open", Address: linkdAddress,
		Sources: []string{"source"}}
	var consoleDown atomic.Bool
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if consoleDown.Load() {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case testLinkdAPIRoot + "/strategy-index/targets":
			_ = json.NewEncoder(w).Encode([]openalerts.TargetBinding{target})
		case testLinkdAPIRoot + "/event-sources/" + target.EventSourceID:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": target.EventSourceID, "revision": 1, "published": 1,
				"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}})
		default:
			q := r.URL.Query()
			tenant, strategyID := q.Get("bk_tenant_id"), q.Get("strategy_id")
			_ = json.NewEncoder(w).Encode(map[string]any{"target": target, "tenantId": tenant, "strategyId": strategyID,
				"key": target.KeyPrefix + ":" + tenant + ":" + strategyID, "complete": true,
				"redis": map[string]any{"complete": true}, "alerts": map[string]any{"complete": true},
				"rows": []any{map[string]any{"fingerprint": fingerprint, "status": "matched",
					"alerts": []openalerts.Alert{{AlertID: "opened", EventSourceID: target.EventSourceID, Fingerprint: fingerprint, Severity: "critical"}}}}})
		}
	}))
	t.Cleanup(console.Close)
	if err := linkdClient.SAdd(context.Background(), target.KeyPrefix+":"+key.TenantID+":"+key.StrategyID, fingerprint).Err(); err != nil {
		t.Fatal(err)
	}

	var cfg config.Config
	cfg.Redis.Mode, cfg.Redis.Address = config.RedisModeStandalone, linkdAddress
	cfg.PhaseTwo.Linkd.ConsoleURL = console.URL + testLinkdAPIRoot
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
	connection := config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: linkdAddress}
	cfg.PhaseTwo.Linkd.Connection, cfg.PhaseTwo.Linkd.KeyPrefix = &connection, target.KeyPrefix
	open := func(config.RedisConnectionConfig) (redis.UniversalClient, bool) { return linkdClient, false }
	startup := &fleet.LinkdDiscoveryFacts{Outcome: fleet.LinkdDiscoveryAdopted, Attempts: 1}
	index, err := newLinkdIndex(cfg, linkdClient, connection, startup, open, nil, clock.now, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := index.Cache
	port.cache = cache
	if err := cache.TrackOwned(key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = cache.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	testwait.Until(t, "the copy read and calibrated the link's set under a confirmed location", func() bool {
		stats := cache.Stats()
		return stats.Available && stats.Calibrated == 1 && stats.Members == 1
	})

	// The broker took the ABNORMAL.
	port.Acknowledged([]contract.TriggerEventV1{*opened.TriggerEvent})
	// Held open past the local retention, a complete calibration that lists
	// the alert takes it out of the recent sends; the record of what this
	// process opened keeps it.
	clock.advance(2 * time.Minute)
	cache.RequestCalibration()
	testwait.Until(t, "a complete calibration past the local retention pruned the send record", func() bool {
		stats := cache.Stats()
		return stats.Added == 0 && stats.OwnOpen == 1
	})

	// The link fails: its Redis is unreachable and its Console answers
	// nothing, past the stale bound the production cadence derives.
	consoleDown.Store(true)
	_ = linkdClient.Close()
	staleAfter := cache.Snapshot(key).StaleAfter
	if staleAfter <= 0 || staleAfter > 10*time.Minute {
		t.Fatalf("setup: the production stale bound is %v", staleAfter)
	}
	clock.advance(staleAfter + 10*time.Second)
	testwait.Until(t, "the set past the stale bound", func() bool { return cache.Snapshot(key).Stale })
	if !cache.Trusted() {
		t.Fatal("setup: the Console's confirmation did not stand through the failures")
	}

	// The series recovers.
	recovered := staleSetEvaluate(t, plan, port, 360, trigger.DetectionNormal, false)
	if recovered.RecordResult != contract.LevelResultRecovery {
		t.Fatalf("setup: the recovered record decided %q, want RECOVERY", recovered.RecordResult)
	}
	if recovered.RecoveryGate.Held || recovered.RecoveryGate.OpenAlertGate != trigger.OpenAlertGatePassed {
		t.Fatalf("recovery gate %+v: the recovery of an alert this process opened was held, and the alert stays open", recovered.RecoveryGate)
	}
	if recovered.TriggerEvent == nil || recovered.TriggerEvent.EventKind != contract.TriggerEventRecovery || recovered.TriggerEvent.DedupeMD5 != fingerprint {
		t.Fatalf("emitted %+v, want the RECOVERY of the opened alert", recovered.TriggerEvent)
	}
}

type staleSetClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *staleSetClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *staleSetClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// staleSetEvaluate evaluates one record of the series at source through the
// evaluator, with port as the open alert set its recovery gate asks.
func staleSetEvaluate(t *testing.T, plan *strategy.CompiledPlan, port *openAlertCopyPort, source int64, result string, anomalous bool) trigger.EvaluationResultV2 {
	t.Helper()
	recordID, err := contract.DeriveRecordIDV2(strings.Repeat("c", 64), source)
	if err != nil {
		t.Fatal(err)
	}
	var facts []trigger.DetectionFact
	var histories []trigger.LevelHistory
	ordinal, algorithm, group := uint32(0), uint32(0), uint32(0)
	for _, level := range plan.Levels().Copy() {
		facts = append(facts, trigger.DetectionFact{Definition: level.Definition(), DetectFingerprint: level.Fingerprints().Detect, Result: result,
			Evidence: trigger.DetectionEvidence{PredicateDigest: level.Detectors()[0].PredicateDigest(), ProjectedValueOrdinal: &ordinal,
				MatchedAlgorithmOrdinal: &algorithm, MatchedGroupOrdinal: &group}})
		histories = append(histories, trigger.LevelHistory{LevelID: level.Definition().LevelID,
			View: staleSetHistory{step: 60, points: map[int64]bool{source: anomalous}}})
	}
	request := trigger.EvaluationRequestV2{
		TenantID: "default", BusinessID: "2", Plan: plan,
		Record: trigger.DetectionRecord{RecordID: recordID, SourceTime: source,
			ProjectedValues: []trigger.ProjectedValue{{CanonicalDecimal: "50.100000", Available: true}}, LevelFacts: facts},
		RecordRef: contract.TriggerRecordRefV1{RecordID: recordID, SourceTime: source, DimensionIdentityDigest: strings.Repeat("c", 64),
			Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"192.0.2.10"`)}},
		Observed:  contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Unit: "percent"},
		Histories: histories, EffectiveTimeFacts: staleSetEffective(t, plan, source),
		EvaluationTime: source, DecisionTime: source, ExecutionID: "execution-1", OpenAlerts: port,
		Limits: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 64, MaxRecoveryConsecutiveWindows: 64,
			MaxRequiredHistoryPoints: 256, MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 64 << 10, MaxComputeCost: 4096},
	}
	evaluated, err := trigger.EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2: %v", err)
	}
	return evaluated
}

func staleSetEffective(t *testing.T, plan *strategy.CompiledPlan, evaluationTime int64) []trigger.LevelEffectiveTimeFact {
	t.Helper()
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return time.UTC, nil
	}))
	levels := plan.Levels().Copy()
	requests := make([]strategy.EffectiveTimeRequest, len(levels))
	for i, level := range levels {
		requests[i] = strategy.EffectiveTimeRequest{TenantID: "default", BusinessID: "2", EvaluationTime: evaluationTime, Requirement: level.EffectiveTimeRequirement()}
	}
	facts, err := provider.Resolve(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]trigger.LevelEffectiveTimeFact, len(levels))
	for i := range levels {
		result[i] = trigger.LevelEffectiveTimeFact{LevelID: levels[i].Definition().LevelID, Fact: facts[i]}
	}
	return result
}

// staleSetPlan is one strategy on the consumer's protocol: a threshold
// Level that opens on one anomaly and recovers on one miss.
func staleSetPlan(t *testing.T) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxRequiredHistoryPoints: 4096,
		MaxTriggerWindowSize: 4096, MaxRecoveryConsecutiveWindows: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "stale-set-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := func(value any) json.RawMessage {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	level := contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: 1, LevelCode: "level", Priority: 1}, Connector: contract.LevelConnectorAND,
		DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: raw(map[string]any{
			"value_field": "value", "data_unit": "percent", "threshold_unit_prefix": "", "precision": map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"},
			"groups": []any{map[string]any{"conditions": []any{map[string]any{"operator": "GTE", "threshold_decimal": "50"}}}},
		})}}},
		TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: raw(map[string]any{"window_size": 1, "required_anomalies": 1, "step_seconds": 60})},
		RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: raw(map[string]any{"enabled": true, "consecutive_windows": 1})},
	}
	ref := contract.StrategyRefV2{TenantID: "default", StrategyID: "1001", Revision: "strategy-r1", SnapshotRevision: 7}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: contract.EvaluationPlanV2{
			PlanID: "1001", StrategyRef: ref, InputProjection: projection, WireFormat: contract.WireFormatStandardRawEvent,
			OutputIdentity: &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}},
			StrategyIR: contract.StrategyIRV2{
				Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: ref,
				ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60,
					EvaluationInterval: 60, LatenessTolerance: 120},
				InputProjection: projection, Levels: []contract.LevelIRV2{level},
			},
		},
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("compiler terminals = %#v / %#v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

// staleSetHistory is a Level's window history held as points, anomalous or
// not, on a fixed step.
type staleSetHistory struct {
	step   int64
	points map[int64]bool
}

func (h staleSetHistory) Summarize(endTime int64, requiredPositions uint32) trigger.HistorySummary {
	start := endTime - int64(requiredPositions-1)*h.step
	summary := trigger.HistorySummary{Completeness: trigger.HistoryFull, WindowStart: start, WindowEnd: endTime}
	digest := sha256.New()
	var encoded [8]byte
	seen := false
	for timestamp := start; timestamp <= endTime; timestamp += h.step {
		anomalous, ok := h.points[timestamp]
		if !ok {
			if seen {
				summary.Completeness = trigger.HistoryGapped
			} else if summary.Completeness == trigger.HistoryFull {
				summary.Completeness = trigger.HistoryWarming
			}
			continue
		}
		seen = true
		summary.ValidPositions++
		if anomalous {
			summary.AnomalyCount++
			binary.BigEndian.PutUint64(encoded[:], uint64(timestamp))
			_, _ = digest.Write(encoded[:])
		}
	}
	copy(summary.AnomalyDigest[:], digest.Sum(nil))
	return summary
}

func (h staleSetHistory) CountAnomalies(fromTime, untilTime int64) uint32 {
	var count uint32
	for timestamp, anomalous := range h.points {
		if anomalous && timestamp >= fromTime && timestamp <= untilTime {
			count++
		}
	}
	return count
}

func (h staleSetHistory) CountObserved(fromTime, untilTime int64) uint32 {
	var count uint32
	for timestamp := range h.points {
		if timestamp >= fromTime && timestamp <= untilTime {
			count++
		}
	}
	return count
}

func (h staleSetHistory) ForEachAnomaly(fromTime, untilTime int64, visit func(int64) bool) {
	times := make([]int64, 0, len(h.points))
	for timestamp, anomalous := range h.points {
		if anomalous && timestamp >= fromTime && timestamp <= untilTime {
			times = append(times, timestamp)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	for _, timestamp := range times {
		if !visit(timestamp) {
			return
		}
	}
}

func (h staleSetHistory) FirstAnomaly(fromTime, untilTime int64) (int64, bool) {
	first, found := int64(0), false
	for timestamp, anomalous := range h.points {
		if !anomalous || timestamp < fromTime || timestamp > untilTime {
			continue
		}
		if !found || timestamp < first {
			first, found = timestamp, true
		}
	}
	return first, found
}

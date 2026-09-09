package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/go-redis/redis/v8"
)

type recordingFinalPublisher struct {
	published      [][]byte
	mu             sync.Mutex
	evidence       []*contract.FinalResultEvidenceV1
	panicOnEnqueue bool
	business       []*contract.BusinessAbnormalV1
	receipts       []*contract.ChainCoverageReceiptV1
	coverageWire   [][]byte
	businessWire   [][]byte
}

func (p *recordingFinalPublisher) TryEnqueueCoverageReceipt(e contract.EncodedGoCoverageV1) bool {
	if p.panicOnEnqueue {
		panic("isolated receipt publisher")
	}
	envelope, err := contract.DecodeGoCoverageRecord(e.CopyBytes(), 1<<20)
	if err != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.receipts = append(p.receipts, &envelope.Receipt)
	p.coverageWire = append(p.coverageWire, e.CopyBytes())
	p.published = append(p.published, e.CopyBytes())
	return true
}

func (p *recordingFinalPublisher) TryEnqueueBusinessAbnormal(e contract.EncodedBusinessAbnormalV1) bool {
	if p.panicOnEnqueue {
		panic("isolated shadow publisher")
	}
	envelope, err := contract.DecodeGoBusinessAbnormalV1(e.CopyBytes(), 1<<20)
	if err != nil {
		panic(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.business = append(p.business, &envelope.Reference)
	p.businessWire = append(p.businessWire, e.CopyBytes())
	p.published = append(p.published, e.CopyBytes())
	return true
}

func (p *recordingFinalPublisher) TryEnqueueEncodedFinalEvidence(e contract.EncodedFinalResultV1) bool {
	if p.panicOnEnqueue {
		panic("isolated shadow publisher")
	}
	b := e.CopyBytes()
	limit := 1 << 20
	copy, err := contract.DecodeShadowResultRecordV1(b, limit)
	if err != nil {
		panic(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evidence = append(p.evidence, copy.Evidence)
	return true
}
func (*recordingFinalPublisher) Shutdown(context.Context) enginekafka.ReceiptDrainResult {
	return enginekafka.ReceiptDrainResult{}
}

func shadowRuntimeManifest(t *testing.T, cfg config.Config, base int64) (context.Context, contract.ValidationEpochManifestV1) {
	t.Helper()
	profile, err := phaseTwoRuntimeProfile(cfg, "test", runtime.GOMAXPROCS(0))
	if err != nil {
		t.Fatal(err)
	}
	m := contract.ValidationEpochManifestV1{
		Schema: contract.Schema{Name: contract.ValidationEpochManifestSchemaV1, Major: 1}, RequiredFeatures: []string{}, EpochID: "runtime-synthetic-epoch",
		StartedAt: base - 60, EligibleFrom: base - 60, ExpectedEnd: base + 600,
		Target:              contract.ShadowTargetScopeV1{TenantID: "tenant-a", BusinessID: strconv.FormatInt(controlledG4SyntheticBusinessID, 10), DataType: "SERIES", Capabilities: []string{"Threshold"}, ExcludedCapabilities: []string{}},
		Python:              contract.ShadowSourceVersionV1{Commit: strings.Repeat("a", 40), Image: "synthetic-python", Schema: "python-final-v1"},
		Go:                  contract.ShadowSourceVersionV1{Commit: commit, Image: "synthetic-go", Schema: "go-final-v1"},
		StrategyObservation: "synthetic-source", StrategyPublication: "synthetic-publication", ComparisonVersion: "comparison-v1", IdentityVersion: "identity-v1",
		PythonTopic: contract.ShadowTopicV1{Name: "python-shadow", ConsumerGroup: "comparison", Partitions: 1}, GoTopic: contract.ShadowTopicV1{Name: "go-shadow", ConsumerGroup: "comparison", Partitions: 1}, AuditTopic: contract.ShadowTopicV1{Name: "audit-shadow", ConsumerGroup: "audit", Partitions: 1},
		Limits:              contract.ShadowResourceLimitsV1{MaxEntries: 100, MaxRetainedBytes: 1 << 20, MaxAgeSeconds: 600, MaxQueueEntries: 10, MaxQueueBytes: 1 << 20, MaxAuditInflight: 1, MaxMessageBytes: 256 << 10},
		GracePolicyRevision: "grace-v1", RuntimeConfigDigests: []string{profile.Digest}, KnownExclusionReasons: []string{},
	}
	return context.WithValue(context.Background(), phaseTwoShadowProfileKey{}, profile), m
}

func TestPhaseTwoShadowActualThresholdACKAndIsolation(t *testing.T) {
	testPhaseTwoShadowActualThresholdACKAndIsolation(t, false)
}
func TestPhaseTwoBusinessActualThresholdACKAndIsolation(t *testing.T) {
	testPhaseTwoShadowActualThresholdACKAndIsolation(t, true)
}
func testPhaseTwoShadowActualThresholdACKAndIsolation(t *testing.T, business bool, modes ...string) {
	mode := ""
	if len(modes) > 0 {
		mode = modes[0]
	}
	queryV3 := strings.HasPrefix(mode, "v3:")
	mode = strings.TrimPrefix(mode, "v3:")
	previousCommit := commit
	commit = strings.Repeat("b", 40)
	t.Cleanup(func() { commit = previousCommit })
	for _, publisherPanics := range []bool{false, true} {
		t.Run(fmt.Sprintf("publisher_panic_%v", publisherPanics), func(t *testing.T) {
			address, client := startPhaseTwoRedis(t)
			base := controlledG4Base(t)
			if queryV3 {
				base = 4102444800
			} // shared controlled source time, all deadlines still derived
			var clock atomic.Int64
			clock.Store(base)
			cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "shadow-runtime")
			if business {
				cfg.PhaseTwo.Control.Timezone = "UTC"
			}
			cfg.PhaseTwo.ShadowManifestPath = filepath.Join(t.TempDir(), "manifest.json")
			ctx, manifest := shadowRuntimeManifest(t, cfg, base)
			if business {
				manifest.ComparisonVersion = "python-business-kafka-v1"
			}
			wire, err := contract.EncodeValidationEpochManifestV1(&manifest, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(cfg.PhaseTwo.ShadowManifestPath, wire, 0600); err != nil {
				t.Fatal(err)
			}
			document := controlledG4StrategyDocument(t, 5101, strategy.DetectorKindThreshold, "usage", "system.cpu", []string{"host"}, []any{[]any{map[string]any{"method": "lt", "threshold": 50}}})
			clientUQ := controlledG4UQClient(t, base, "system.cpu")
			if business {
				wire, readErr := os.ReadFile("../../contract/testdata/business-kafka-v1/snapshots.json")
				if readErr != nil {
					t.Fatal(readErr)
				}
				var snapshots map[string]struct {
					Strategy map[string]any `json:"strategy"`
				}
				if err = json.Unmarshal(wire, &snapshots); err != nil {
					t.Fatal(err)
				}
				source := snapshots["snapshot-fixture"].Strategy
				if queryV3 {
					raw, err := os.ReadFile("../../contract/testdata/query-v3/strategy.json")
					if err != nil {
						t.Fatal(err)
					}
					if err = json.Unmarshal(raw, &source); err != nil {
						t.Fatal(err)
					}
				}

				// Only native routing identity differs from the public Python source;
				// the actual selector/detector/units/schedule closure is unchanged.
				source["id"] = 5101
				source["bk_biz_id"] = controlledG4SyntheticBusinessID
				source["bk_tenant_id"] = "tenant-a"
				source["space_uid"] = controlledG4SyntheticSpaceUID
				document, err = json.Marshal(source)
				if err != nil {
					t.Fatal(err)
				}
				clientUQ = &http.Client{Transport: controlledRoundTripper(func(req *http.Request) (*http.Response, error) {
					p, err := decodeControlledG4UQRequest(req)
					if err != nil {
						return nil, err
					}
					end, err := strconv.ParseInt(p.EndTime, 10, 64)
					if err != nil {
						return nil, err
					}
					series := controlledG4UQSeries(base, end, "system.cpu", p.MetricMerge)
					value := 0.0
					if end == base && mode == "" {
						value = 81
					}
					series["values"] = []any{[]any{(end - 1) * 1000, value}}
					if mode == "query_failure" {
						return nil, fmt.Errorf("controlled source failure")
					}
					if mode == "empty" {
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"series":[],"status":null,"trace_id":"receipt-empty","is_partial":false,"result_table_id":["system.cpu"]}`)), Request: req}, nil
					}
					return controlledG4UQResponse(req, "system.cpu", series)
				})}
			}

			var displayConfig map[string]any
			if err := json.Unmarshal(document, &displayConfig); err != nil {
				t.Fatal(err)
			}
			displayConfig["name"] = "controlled threshold"
			displayConfig["scenario"] = "os"
			for _, rawItem := range displayConfig["items"].([]any) {
				item := rawItem.(map[string]any)
				if item["name"] == nil {
					item["name"] = "usage"
				}
			}
			document, _ = json.Marshal(displayConfig)
			for key, value := range map[string]string{"alarm-config.strategy_ids": "[5101]", "alarm-config.strategy_5101": string(document)} {
				if err = client.Set(ctx, key, value, 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			events := &legacyConvertingTestSink{recordingPhaseTwoEventSink: &recordingPhaseTwoEventSink{}}
			// Omit the topic: conversion must be assembled without an enable field.
			cfg.Kafka.LegacyAdapter = config.LegacyAdapterConfig{SnapshotPrefix: "test", ServiceNodes: map[string]config.RedisConnectionConfig{"service": cfg.StrategySourceRedis()}, ServiceRoutes: []legacyoutput.ServiceRoute{{UpperBound: 1000000, NodeID: "service"}}}
			publisher := &recordingFinalPublisher{panicOnEnqueue: publisherPanics}
			var observations []observability.Observation
			var mu sync.Mutex
			bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
				func(c redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
					return controlplane.NewLegacyRedisStrategySource(c, prefix)
				},
				phaseTwoProductionExternalDependencies{Now: func() time.Time { return time.Unix(clock.Load(), 0) }, HTTPClient: clientUQ,
					OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
					OpenFinalEvidence: func(c enginekafka.DecisionSinkConfig, _ enginekafka.ReceiptPublisherLimits, _ enginekafka.ReceiptPublisherDiagnostics) (phaseTwoFinalPublisher, error) {
						if c.OutputTopic != manifest.GoTopic.Name {
							t.Errorf("wrong shadow topic")
						}
						return publisher, nil
					},
					AdditionalObserver: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
						mu.Lock()
						defer mu.Unlock()
						observations = append(observations, o)
					}),
				})
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Shutdown(ctx)
			if err = bundle.Start(ctx); err != nil {
				t.Fatal(err)
			}
			for _, evaluation := range []int64{base, base + 60} {
				clock.Store(evaluation + 1)
				if err = bundle.runScheduledOnce(ctx); err != nil && mode != "query_failure" {
					t.Fatal(err)
				}
			}
			native := events.snapshot()
			if mode != "" {
				for _, event := range native {
					if event.EventKind == contract.TriggerEventAbnormal || mode == "empty" {
						t.Fatal("normal/empty invented abnormal", event.EventKind)
					}
				}
				publisher.mu.Lock()
				defer publisher.mu.Unlock()
				if publisherPanics {
					if len(publisher.receipts) != 0 {
						t.Fatal("panic publisher emitted")
					}
					return
				}
				if queryV3 && !publisherPanics {
					assertV3RuntimeCoverage(t, publisher, manifest)
				}
				if len(publisher.receipts) == 0 {
					t.Fatal("missing actual receipt")
				}
				for _, r := range publisher.receipts {
					if mode == "query_failure" {
						if r.CoverageComplete || r.Input.Completion != "UNAVAILABLE" || r.Input.QueryAttempts.CurrentExecution.Value == nil {
							t.Fatalf("failure receipt: %+v", r)
						}
						continue
					}
					if !r.CoverageComplete || !r.TerminalFact || r.Input.Completion != "FULL" || *r.Records.PrimaryAbnormal.Value != 0 {
						t.Fatalf("normal/empty receipt: %+v", r)
					}
					if mode == "empty" && (*r.Input.Series.Value != 0 || *r.Input.SelectedPlanRecords.Value != 0) {
						t.Fatal("FULL_EMPTY invented series/record")
					}
					if mode == "normal" && *r.Input.SelectedPlanRecords.Value == 0 {
						t.Fatal("normal record count absent")
					}
				}
				return
			}
			wantNative := 2
			if queryV3 {
				wantNative = 1
			}
			if len(native) != wantNative {
				b, _ := json.Marshal(observations)
				t.Fatalf("native=%d observations=%s", len(native), b)
			}
			if native[0].EvaluationTime != base || native[0].EventKind != contract.TriggerEventAbnormal || (!queryV3 && (native[0].EventID == native[1].EventID || native[1].EvaluationTime != base+60 || native[1].EventKind != contract.TriggerEventRecovery)) {
				t.Fatalf("two distinct Slot results required: %+v", native)
			}
			for _, event := range native {
				if event.LegacyOutput == nil || event.LegacyOutput.Configuration == nil {
					t.Fatal("catalog legacy context did not reach production Trigger")
				}
				if event.EventKind == contract.TriggerEventAbnormal && (len(event.LegacyOutput.AnomalyTimestamps) != 1 || event.LegacyOutput.AnomalyTimestamps[0] != event.RecordRef.SourceTime) {
					t.Fatal("actual primary anomaly timestamp lost")
				}
				if event.EventKind == contract.TriggerEventRecovery && len(event.LegacyOutput.AnomalyTimestamps) != 0 {
					t.Fatal("recovery invented anomaly timestamps")
				}
			}
			production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
			progress := loadPhaseTwoProgress(t, ctx, production, bundle.queryGroups[0])
			if progress.LastFullSlot != execution.EvaluationTime(base+60) || progress.NextSlot != execution.EvaluationTime(base+120) {
				t.Fatalf("shadow affected Progress: %+v", progress)
			}
			keys, err := client.Keys(ctx, cfg.Redis.StatePrefix+":runtime:v2:*").Result()
			if err != nil || len(keys) == 0 {
				t.Fatalf("State missing: %v %v", keys, err)
			}
			publisher.mu.Lock()
			defer publisher.mu.Unlock()
			want := 2
			if business {
				want = 1
			}
			if queryV3 {
				want = 0
			}
			if publisherPanics {
				want = 0
			}
			if len(publisher.evidence) != want {
				b, _ := json.Marshal(observations)
				t.Fatalf("final=%d want=%d observations=%s", len(publisher.evidence), want, b)
			}
			if business && !publisherPanics {
				if len(publisher.receipts) != 2 {
					t.Fatalf("actual Slot receipts=%d want2", len(publisher.receipts))
				}
				for _, receipt := range publisher.receipts {
					if !receipt.CoverageComplete {
						t.Fatalf("receipt incomplete: %+v", receipt)
					}
				}
				if len(publisher.business) != 1 || publisher.business[0].Native.EventID != native[0].EventID {
					t.Fatal("actual ABNORMAL business profile absent")
				}
				if !queryV3 && publisher.business[0].ConfigDigest != "c80841863ec8277a1b4e0a408df335ebccdaec4ed5e816fc5517705b7004b735" {
					t.Fatal("actual Python/Go config closure differs", string(publisher.business[0].Config))
				}
				if queryV3 {
					assertV3RuntimeCoverage(t, publisher, manifest)
				}
				if publisher.business[0].Primary.Status != "ABNORMAL" {
					t.Fatal("Recovery entered business equivalence")
				}
			}
			for i, e := range publisher.evidence {
				if business {
					i++
				}
				if e.Native.EventID != native[i].EventID || e.Native.SemanticDigest != native[i].EventSemanticDigest || !e.Delivery.BusinessACK {
					t.Fatalf("wrong actual ACK association: %+v", e)
				}
				if e.Primary.Unit != "" {
					t.Fatalf("known empty unit=%q", e.Primary.Unit)
				}
			}
		})
	}
}

type legacyConvertingTestSink struct {
	*recordingPhaseTwoEventSink
	converter enginekafka.LegacyEventConverter
}

func (s *legacyConvertingTestSink) ConfigureLegacyOutput(converter enginekafka.LegacyEventConverter, topic string, _ int) error {
	if topic != "alarmd_0bkmonitor_backend_event" {
		return fmt.Errorf("unexpected Python output topic: %s", topic)
	}
	s.converter = converter
	return nil
}
func (s *legacyConvertingTestSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	if s.converter == nil {
		return fmt.Errorf("bundle did not configure legacy converter")
	}
	converted, err := s.converter.ConvertBatch(ctx, events)
	if err != nil {
		return err
	}
	if len(converted) != len(events) {
		return fmt.Errorf("converter lost events")
	}
	return s.recordingPhaseTwoEventSink.WriteBatch(ctx, events)
}

type failedShadowNativeSink struct {
	*recordingPhaseTwoEventSink
	err     error
	partial int
}

func (s failedShadowNativeSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	// A batch implementation may have sent a prefix before its aggregate error.
	// The caller receives no successful subset and must not claim one.
	s.mu.Lock()
	s.events = append(s.events, events[:s.partial]...)
	s.mu.Unlock()
	return s.err
}

func TestPhaseTwoShadowFailedBatchDoesNotInventPartialACK(t *testing.T) {
	want := errors.New("batch has no per-event ACK result")
	for _, partial := range []int{0, 1} {
		publisher := &recordingFinalPublisher{panicOnEnqueue: true}
		native := failedShadowNativeSink{recordingPhaseTwoEventSink: &recordingPhaseTwoEventSink{}, err: want, partial: partial}
		observed := 0
		emitter := &phaseTwoFinalEmitter{publisher: publisher, manifest: contract.ValidationEpochManifestV1{EligibleFrom: 0, ExpectedEnd: 200}, observer: observability.ObserverFunc(func(context.Context, observability.Observation) { observed++ })}
		sink := phaseTwoShadowEventSink{productionPhaseTwoEventSink: native, emitter: emitter}
		ctx := context.WithValue(context.Background(), phaseTwoShadowExecutionKey{}, &phaseTwoShadowExecution{})
		batch := []contract.TriggerEventV1{{EventID: "first", EvaluationTime: 100}, {EventID: "second", EvaluationTime: 100}}
		if got := sink.WriteBatch(ctx, batch); got != want {
			t.Fatalf("ACK error changed: %v", got)
		}
		if len(publisher.evidence) != 0 || observed != 0 || len(native.snapshot()) != partial {
			t.Fatal("partial business send mistaken for complete batch ACK")
		}
		native.err = nil
		sink.productionPhaseTwoEventSink = native
		if err := sink.WriteBatch(ctx, batch); err != nil || observed != 2 {
			t.Fatal("successful batch did not reach evidence observation")
		}
	}
}

func TestPhaseTwoShadowManifestBindsActualProfileAndBounds(t *testing.T) {
	previous := commit
	commit = strings.Repeat("b", 40)
	t.Cleanup(func() { commit = previous })
	cfg := validGoAccessRuntimeConfig()
	if m, err := loadPhaseTwoShadowManifest(context.Background(), cfg); err != nil || m != nil {
		t.Fatalf("disabled requires extra facts: %v", err)
	}
	cfg.PhaseTwo.ShadowManifestPath = filepath.Join(t.TempDir(), "manifest.json")
	ctx, original := shadowRuntimeManifest(t, cfg, 1000)
	for _, test := range []struct {
		name   string
		change func(*contract.ValidationEpochManifestV1)
		ctx    context.Context
		bad    bool
	}{
		{"valid", func(*contract.ValidationEpochManifestV1) {}, ctx, false},
		{"missing_actual_profile", func(*contract.ValidationEpochManifestV1) {}, context.Background(), true},
		{"different_profile", func(m *contract.ValidationEpochManifestV1) {
			m.RuntimeConfigDigests = []string{strings.Repeat("e", 64)}
		}, ctx, true},
		{"different_commit", func(m *contract.ValidationEpochManifestV1) { m.Go.Commit = strings.Repeat("a", 40) }, ctx, true},
		{"queue_bytes", func(m *contract.ValidationEpochManifestV1) {
			m.Limits.MaxQueueBytes = uint64(cfg.ReceiptQueue.MaxQueuedBytes) + 1
		}, ctx, true},
		{"message_bytes", func(m *contract.ValidationEpochManifestV1) {
			m.Limits.MaxMessageBytes = uint64(cfg.Kafka.TriggerEvent.MaxMessageBytes) + 1
		}, ctx, true},
		{"native_topic", func(m *contract.ValidationEpochManifestV1) { m.GoTopic.Name = cfg.Kafka.TriggerEvent.Topic }, ctx, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := original
			test.change(&m)
			wire, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(cfg.PhaseTwo.ShadowManifestPath, wire, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = loadPhaseTwoShadowManifest(test.ctx, cfg)
			if (err != nil) != test.bad {
				t.Fatalf("err=%v bad=%v", err, test.bad)
			}
		})
	}
}

func assertV3RuntimeCoverage(t *testing.T, p *recordingFinalPublisher, manifest contract.ValidationEpochManifestV1) {
	t.Helper()
	for _, wire := range p.coverageWire {
		e, err := contract.DecodeGoCoverageEnvelopeV2(wire, 1<<20)
		if err != nil || !e.Receipt.CoverageComplete || e.CompletedAt == nil || len(e.Config.Query.Selectors) != 2 {
			t.Fatalf("actual v3 coverage: %v", err)
		}
	}
	for _, wire := range p.businessWire {
		e, err := contract.DecodeGoBusinessAbnormalV1(wire, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			Schema string `json:"schema_version"`
		}
		if err = json.Unmarshal(e.Reference.Config, &c); err != nil || c.Schema != contract.BusinessAbnormalConfigV2 {
			t.Fatal("actual v3 business config absent", err)
		}
	}
	assertV3RuntimeConsumer(t, p, manifest)
}
func TestPhaseTwoBusinessV3ActualACKAndZeroAbnormalReceipt(t *testing.T) {
	for _, mode := range []string{"v3:", "v3:normal", "v3:empty"} {
		t.Run(mode, func(t *testing.T) { testPhaseTwoShadowActualThresholdACKAndIsolation(t, true, mode) })
	}
}

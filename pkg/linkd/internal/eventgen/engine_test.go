// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/cleaner"
	linkdconfig "linkd/internal/config"
	"linkd/internal/domain"
)

type fakePublisher struct {
	batches [][]Record
	err     error
}

func (p *fakePublisher) Publish(_ context.Context, records []Record) error {
	cloned := make([]Record, len(records))
	for index, record := range records {
		cloned[index] = record
		cloned[index].Key = append([]byte(nil), record.Key...)
		cloned[index].Body = append([]byte(nil), record.Body...)
		cloned[index].Headers = make(map[string]string, len(record.Headers))
		for key, value := range record.Headers {
			cloned[index].Headers[key] = value
		}
	}
	p.batches = append(p.batches, cloned)
	return p.err
}

type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Wait(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.waits = append(c.waits, duration)
	c.now = c.now.Add(duration)
	return nil
}

func TestRunCycleRateAndGeometricRecovery(t *testing.T) {
	t.Parallel()
	publisher := &fakePublisher{}
	engine := newTestEngine(t, publisher, func(cfg *Config) {
		cfg.NewAlertsPerMinute = 20
		cfg.CycleDuration = 30 * time.Second
		cfg.MeanLifetimeCycles = 1
	})
	startedAt := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

	first, err := engine.RunCycle(context.Background(), 1, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generated != 10 || first.Resolved != 0 || first.Active != 10 || first.Published != 10 {
		t.Fatalf("first cycle = %#v", first)
	}
	second, err := engine.RunCycle(context.Background(), 2, startedAt.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.Generated != 10 || second.Resolved != 10 || second.Active != 10 || second.Published != 20 {
		t.Fatalf("second cycle = %#v", second)
	}
	if len(publisher.batches) != 2 || len(publisher.batches[0]) != 10 || len(publisher.batches[1]) != 20 {
		t.Fatalf("published batch sizes = %v", batchSizes(publisher.batches))
	}

	triggerFingerprints := make(map[string]string)
	for _, record := range publisher.batches[0] {
		assertValidRecord(t, record)
		if record.Action != domain.EventActionTriggered {
			t.Fatalf("first cycle action = %q", record.Action)
		}
		triggerFingerprints[record.SourceAlertID] = record.Fingerprint
	}
	for index, record := range publisher.batches[1] {
		assertValidRecord(t, record)
		if index < 10 {
			if record.Action != domain.EventActionResolved {
				t.Fatalf("second cycle record %d action = %q, want resolved", index, record.Action)
			}
			if record.Fingerprint != triggerFingerprints[record.SourceAlertID] {
				t.Fatalf("alert %q fingerprint changed", record.SourceAlertID)
			}
		} else if record.Action != domain.EventActionTriggered {
			t.Fatalf("second cycle record %d action = %q, want triggered", index, record.Action)
		}
	}
}

func TestFractionalRateAccumulatorDoesNotDrift(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, &fakePublisher{}, func(cfg *Config) {
		cfg.NewAlertsPerMinute = 1
		cfg.CycleDuration = 20 * time.Second
		cfg.MeanLifetimeCycles = 1000
	})
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	want := []int{0, 0, 1, 0, 0, 1}
	for index, expected := range want {
		result, err := engine.RunCycle(context.Background(), index+1, now.Add(time.Duration(index)*20*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if result.Generated != expected {
			t.Fatalf("cycle %d generated = %d, want %d", index+1, result.Generated, expected)
		}
	}
}

func TestRunCycleDuplicatesExactRecords(t *testing.T) {
	t.Parallel()
	publisher := &fakePublisher{}
	engine := newTestEngine(t, publisher, func(cfg *Config) {
		cfg.DuplicatePercent = 100
		cfg.MeanLifetimeCycles = 1000
	})
	result, err := engine.RunCycle(context.Background(), 1, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Generated != 10 || result.Duplicated != 10 || result.Published != 20 {
		t.Fatalf("cycle=%#v", result)
	}
	for index := 0; index < len(publisher.batches[0]); index += 2 {
		if !reflect.DeepEqual(publisher.batches[0][index], publisher.batches[0][index+1]) {
			t.Fatalf("record %d duplicate changed payload", index)
		}
	}
}

func TestRunUsesImmediateFirstCycleAndFakeClock(t *testing.T) {
	t.Parallel()
	publisher := &fakePublisher{}
	engine := newTestEngine(t, publisher, func(cfg *Config) {
		cfg.Cycles = 2
		cfg.MeanLifetimeCycles = 1
	})
	clock := &fakeClock{now: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)}
	engine.clock = clock
	if err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(clock.waits) != 1 || clock.waits[0] != 30*time.Second {
		t.Fatalf("clock waits = %v", clock.waits)
	}
	if len(publisher.batches) != 2 {
		t.Fatalf("published batches = %d, want 2", len(publisher.batches))
	}
}

func TestCycleCapacityAndPublishFailureDoNotCommitState(t *testing.T) {
	t.Parallel()
	t.Run("capacity", func(t *testing.T) {
		t.Parallel()
		engine := newTestEngine(t, &fakePublisher{}, func(cfg *Config) { cfg.MaxActiveAlerts = 9 })
		_, err := engine.RunCycle(context.Background(), 1, time.Now().UTC())
		if err == nil || !strings.Contains(err.Error(), "capacity exceeded") {
			t.Fatalf("RunCycle() error = %v", err)
		}
		if len(engine.active) != 0 {
			t.Fatalf("active alerts = %d after capacity failure", len(engine.active))
		}
	})
	t.Run("publisher", func(t *testing.T) {
		t.Parallel()
		publisher := &fakePublisher{err: errors.New("broker unavailable")}
		engine := newTestEngine(t, publisher, nil)
		_, err := engine.RunCycle(context.Background(), 1, time.Now().UTC())
		if err == nil || !strings.Contains(err.Error(), "broker unavailable") {
			t.Fatalf("RunCycle() error = %v", err)
		}
		if len(engine.active) != 0 || engine.totalSent != 0 {
			t.Fatalf("state committed after publish failure: active=%d sent=%d", len(engine.active), engine.totalSent)
		}
	})
}

func TestRunTreatsCancellationAsGracefulStop(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, &fakePublisher{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Run(ctx); err != nil {
		t.Fatalf("Run() cancellation error = %v", err)
	}
}

func newTestEngine(t *testing.T, publisher Publisher, mutate func(*Config)) *Engine {
	t.Helper()
	cfg := Config{
		RunID: "test-run", TenantID: "tenant-a",
		NewAlertsPerMinute: 20, CycleDuration: 30 * time.Second, MeanLifetimeCycles: 4,
		Scenarios: []Scenario{ScenarioCPUHigh, ScenarioDiskFull, ScenarioOOMKilled},
		Seed:      42, MaxActiveAlerts: 1000, Cycles: 0,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	engine, err := New(cfg, testSource(), linkdconfig.DefaultSeverityConfig(), publisher, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func assertValidRecord(t *testing.T, record Record) {
	t.Helper()
	if len(record.Key) == 0 || string(record.Key) != record.Fingerprint {
		t.Fatalf("record key = %q, fingerprint = %q", record.Key, record.Fingerprint)
	}
	for _, key := range []string{"message_id", "bk_tenant_id", "order_key"} {
		if record.Headers[key] == "" {
			t.Fatalf("record missing header %q", key)
		}
	}
	if record.Headers["message_id"] != record.SourceEventID {
		t.Fatalf("message_id = %q, source event = %q", record.Headers["message_id"], record.SourceEventID)
	}
	if _, err := (cleaner.StandardCleaner{}).Clean(context.Background(), cleaner.RawEventMessage{Payload: record.Body}); err != nil {
		t.Fatalf("generated standard payload is invalid: %v", err)
	}
}

func batchSizes(batches [][]Record) []int {
	result := make([]int, len(batches))
	for index, batch := range batches {
		result[index] = len(batch)
	}
	return result
}

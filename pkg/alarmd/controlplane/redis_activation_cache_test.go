package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/go-redis/redis/v8"
)

type activationReadClient struct {
	redis.Cmdable
	payload string
	err     error
	reads   atomic.Int64
}

func (c *activationReadClient) Get(context.Context, string) *redis.StringCmd {
	c.reads.Add(1)
	return redis.NewStringResult(c.payload, c.err)
}

func activationCachePayload(t *testing.T, draining int) string {
	t.Helper()
	p := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1"}
	current := SnapshotPublicationRef{SnapshotRevision: "current", PublicationEpoch: 1}
	s := ActivationState{SchemaVersion: legacyActivationSchemaVersion, RecordRevision: 1, Current: current,
		Pending: &SnapshotPublicationRef{SnapshotRevision: "pending", PublicationEpoch: 2},
		Plans: []PlanActivationRecord{{Publication: current, Fact: execution.PlanActivationFact{Plan: p, Selection: execution.ActivationCurrent,
			Selected: execution.ActivatedPlan{Identity: p, StateGeneration: "state", StateApplyEpoch: 1, ScheduleRevision: "schedule", RequiredFullSlots: 1}}}}}
	for i := 0; i < draining; i++ {
		s.Draining = append(s.Draining, DrainingQueryGroup{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("retired-%d", i)), RetiredBoundary: 60})
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestActivationCacheAvoidsRepeatedParsingWithoutElidingReads(t *testing.T) {
	c := &activationReadClient{payload: activationCachePayload(t, 1000)}
	r, _ := NewRedisCatalogRepository(c, "test", time.Hour)
	if _, err := r.LoadActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := c.reads.Load()
	allocs := testing.AllocsPerRun(10, func() {
		if _, err := r.LoadActivation(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	if c.reads.Load()-before != 11 {
		t.Fatal("live GET was skipped")
	}
	if allocs > 20 {
		t.Fatalf("warm read allocations=%.0f, want <=20 without repeated payload parsing", allocs)
	}
}

func TestActivationCacheIsolationAndLiveChanges(t *testing.T) {
	c := &activationReadClient{payload: activationCachePayload(t, 1)}
	r, _ := NewRedisCatalogRepository(c, "test", time.Hour)
	ctx := context.Background()
	a, err := r.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.Pending.SnapshotRevision = "mutated"
	a.Plans[0].Fact.Selected.StateGeneration = "mutated"
	a.Draining[0].RetiredBoundary = 1
	b, err := r.LoadActivation(ctx)
	if err != nil || b.Pending.SnapshotRevision != "pending" || b.Plans[0].Fact.Selected.StateGeneration != "state" || b.Draining[0].RetiredBoundary != 60 {
		t.Fatal("caller corrupted cached facts", err)
	}
	b.Plans[0].Fact.Selected.StateGeneration = "changed-same-revision"
	raw, _ := json.Marshal(b)
	c.payload = string(raw)
	changed, err := r.LoadActivation(ctx)
	if err != nil || changed.Plans[0].Fact.Selected.StateGeneration != "changed-same-revision" {
		t.Fatal("same revision hid live change", err)
	}
	c.payload = `{"record_revision":1}`
	if _, err = r.LoadActivation(ctx); err == nil {
		t.Fatal("cached state hid corruption")
	}
	c.payload = activationCachePayload(t, 1)
	c.err = redis.Nil
	if _, err = r.LoadActivation(ctx); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatal("cached state hid deletion", err)
	}
	c.err = errors.New("disconnected")
	var dependency *ActivationDependencyIOError
	if _, err = r.LoadActivation(ctx); !errors.As(err, &dependency) {
		t.Fatal("cached state hid dependency error", err)
	}
	c.err = nil
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				v, err := r.LoadActivation(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				v.Plans[0].Fact.Selected.StateGeneration = "private"
			}
		}()
	}
	wg.Wait()
}

func TestActivationCacheBoundDoesNotRejectValidLargePayload(t *testing.T) {
	cache := &parsedActivationCache{}
	small := activationCachePayload(t, 1)
	if _, err := cache.load(small); err != nil {
		t.Fatal(err)
	}
	large := small + strings.Repeat(" ", parsedActivationMaxPayloadBytes)
	entry, err := cache.load(large)
	if err != nil || entry.state.RecordRevision != 1 {
		t.Fatal("valid large payload rejected", err)
	}
	if cache.entry != nil {
		t.Fatal("oversized payload or previous entry was retained")
	}
	if _, err := cache.load(small); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.load(`{"bad":`); err == nil || cache.entry != nil {
		t.Fatal("corruption retained cached state")
	}
}

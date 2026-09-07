package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/go-redis/redis/v8"
)

// keyedReadClient answers GET and STRLEN from a key/value map and counts the
// reads per key so tests can prove which bodies were fetched.
type keyedReadClient struct {
	redis.Cmdable
	mu      sync.Mutex
	values  map[string]string
	err     error
	gets    map[string]int
	strlens map[string]int
}

func newKeyedReadClient() *keyedReadClient {
	return &keyedReadClient{values: map[string]string{}, gets: map[string]int{}, strlens: map[string]int{}}
}

func (c *keyedReadClient) set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
}

func (c *keyedReadClient) del(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, key)
}

func (c *keyedReadClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

func (c *keyedReadClient) getCount(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets[key]
}

func (c *keyedReadClient) strlenCount(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.strlens[key]
}

func (c *keyedReadClient) Get(ctx context.Context, key string) *redis.StringCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets[key]++
	if c.err != nil {
		return redis.NewStringResult("", c.err)
	}
	value, ok := c.values[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, ctx.Err())
}

func (c *keyedReadClient) StrLen(ctx context.Context, key string) *redis.IntCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.strlens[key]++
	if c.err != nil {
		return redis.NewIntResult(0, c.err)
	}
	return redis.NewIntResult(int64(len(c.values[key])), ctx.Err())
}

func activationCachePayload(t *testing.T, draining int) string {
	t.Helper()
	return activationCachePayloadAt(t, draining, 1)
}

func activationCachePayloadAt(t *testing.T, draining int, revision uint64) string {
	t.Helper()
	p := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1"}
	current := SnapshotPublicationRef{SnapshotRevision: "current", PublicationEpoch: 1}
	s := ActivationState{SchemaVersion: legacyActivationSchemaVersion, RecordRevision: revision, Current: current,
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

func activationHeaderFor(t *testing.T, payload string) string {
	t.Helper()
	var state ActivationState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		t.Fatal(err)
	}
	header, err := activationHeader(state.RecordRevision, state.Current, state.Pending)
	if err != nil {
		t.Fatal(err)
	}
	return header
}

func newActivationCacheFixture(t *testing.T, payload string) (*keyedReadClient, *RedisCatalogRepository) {
	t.Helper()
	c := newKeyedReadClient()
	r, err := NewRedisCatalogRepository(c, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c.set(r.activationHeaderKey(), activationHeaderFor(t, payload))
	c.set(r.activationKey(), payload)
	return c, r
}

func TestActivationVersionCacheReadsBodyOncePerHeader(t *testing.T) {
	payload := activationCachePayload(t, 1000)
	c, r := newActivationCacheFixture(t, payload)
	ctx := context.Background()
	if _, err := r.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.getCount(r.activationKey()); got != 1 {
		t.Fatalf("cold read fetched activation body %d times, want 1", got)
	}
	allocs := testing.AllocsPerRun(10, func() {
		if _, err := r.LoadActivation(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if got := c.getCount(r.activationHeaderKey()); got != 12 {
		t.Fatalf("live header probe count=%d, want one per read (12)", got)
	}
	if got := c.strlenCount(r.activationKey()); got != 12 {
		t.Fatalf("live activation length probe count=%d, want one per read (12)", got)
	}
	if got := c.getCount(r.activationKey()); got != 1 {
		t.Fatalf("warm reads fetched activation body: total=%d, want 1", got)
	}
	if allocs > 40 {
		t.Fatalf("warm read allocations=%.0f, want <=40 without repeated payload parsing or body transfer", allocs)
	}
	stats := r.ControlReadCacheStats().Activation
	if stats.Misses != 1 || stats.Hits != 11 || stats.Refreshes != 0 {
		t.Fatalf("activation cache stats=%+v, want 1 miss and 11 hits", stats)
	}
}

func TestActivationVersionCacheInvalidatesOnHeaderChange(t *testing.T) {
	first := activationCachePayloadAt(t, 1, 1)
	c, r := newActivationCacheFixture(t, first)
	ctx := context.Background()
	if _, err := r.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	// A cutover writes the header and the body in one script; the next read
	// must observe the new body after one refresh read.
	second := activationCachePayloadAt(t, 2, 2)
	c.set(r.activationKey(), second)
	c.set(r.activationHeaderKey(), activationHeaderFor(t, second))
	state, err := r.LoadActivation(ctx)
	if err != nil || state.RecordRevision != 2 || len(state.Draining) != 2 {
		t.Fatalf("header change served stale activation: %+v %v", state, err)
	}
	if got := c.getCount(r.activationKey()); got != 2 {
		t.Fatalf("body reads after header change=%d, want 2", got)
	}
	if _, err := r.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.getCount(r.activationKey()); got != 2 {
		t.Fatalf("body reads after refresh=%d, want 2", got)
	}
	stats := r.ControlReadCacheStats().Activation
	if stats.Misses != 1 || stats.Refreshes != 1 || stats.Hits != 1 {
		t.Fatalf("activation cache stats=%+v, want 1 miss, 1 refresh, 1 hit", stats)
	}
}

func TestActivationVersionCacheDetectsBodyLengthChangeUnderSameHeader(t *testing.T) {
	payload := activationCachePayload(t, 1)
	c, r := newActivationCacheFixture(t, payload)
	ctx := context.Background()
	if _, err := r.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	var state ActivationState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		t.Fatal(err)
	}
	state.Plans[0].Fact.Selected.StateGeneration = "changed-same-revision"
	changed, _ := json.Marshal(state)
	c.set(r.activationKey(), string(changed))
	loaded, err := r.LoadActivation(ctx)
	if err != nil || loaded.Plans[0].Fact.Selected.StateGeneration != "changed-same-revision" {
		t.Fatal("same header with a different body length was served stale", err)
	}
	if got := c.getCount(r.activationKey()); got != 2 {
		t.Fatalf("body reads=%d, want 2", got)
	}
}

func TestActivationVersionCacheSurfacesMissingAndFailingActivation(t *testing.T) {
	payload := activationCachePayload(t, 1)
	c, r := newActivationCacheFixture(t, payload)
	ctx := context.Background()
	if _, err := r.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	c.del(r.activationKey())
	if _, err := r.LoadActivation(ctx); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatal("cached activation hid deletion under an unchanged header", err)
	}
	if got := c.getCount(r.activationKey()); got != 1 {
		t.Fatalf("missing activation caused a body read: reads=%d, want 1", got)
	}
	c.set(r.activationKey(), payload)
	if _, err := r.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.getCount(r.activationKey()); got != 2 {
		t.Fatalf("deletion did not clear the cached activation: reads=%d, want 2", got)
	}
	c.set(r.activationKey(), `{"record_revision":1}`)
	if _, err := r.LoadActivation(ctx); err == nil {
		t.Fatal("cached state hid corruption")
	}
	c.set(r.activationKey(), payload)
	c.fail(errors.New("disconnected"))
	var dependency *ActivationDependencyIOError
	if _, err := r.LoadActivation(ctx); !errors.As(err, &dependency) {
		t.Fatal("cached state hid dependency error", err)
	}
	c.fail(nil)
	if _, err := r.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestActivationVersionCacheDisabledWithoutHeader(t *testing.T) {
	payload := activationCachePayload(t, 1)
	c := newKeyedReadClient()
	r, err := NewRedisCatalogRepository(c, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c.set(r.activationKey(), payload)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := r.LoadActivation(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.getCount(r.activationKey()); got != 3 {
		t.Fatalf("without a header every read must fetch the body: reads=%d, want 3", got)
	}
	if got := c.strlenCount(r.activationKey()); got != 0 {
		t.Fatalf("without a header no length probe is needed: strlen=%d", got)
	}
	c.del(r.activationKey())
	if _, err := r.LoadActivation(ctx); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatal("missing activation without header", err)
	}
}

func TestActivationVersionCacheIsolatesCallersAndRaces(t *testing.T) {
	payload := activationCachePayload(t, 1)
	_, r := newActivationCacheFixture(t, payload)
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

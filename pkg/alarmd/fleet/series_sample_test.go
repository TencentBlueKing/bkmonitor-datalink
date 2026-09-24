package fleet

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/go-redis/redis/v8"
)

func fleetSampleFixture(t *testing.T) (*observability.SeriesSampler, observability.SeriesSampleCandidate) {
	t.Helper()
	s, err := observability.NewSeriesSampler(observability.SeriesSampleLimits{RecordsPerMinute: 8, BytesPerMinute: 8 * observability.SeriesSampleMaxBytes, QueueCapacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	v := observability.SeriesSampleSelection{QueryGroup: digest("a"), WindowID: "window", OpenedAt: now, ExpiresAt: now.Add(time.Minute), TenantID: "tenant", BusinessID: "business", StrategyID: "strategy", StateGeneration: "generation", PlanScheduleRevision: "schedule"}
	if err := s.Select([]observability.SeriesSampleSelection{v}); err != nil {
		t.Fatal(err)
	}
	return s, observability.SeriesSampleCandidate{QueryGroup: v.QueryGroup, TenantID: v.TenantID, BusinessID: v.BusinessID, StrategyID: v.StrategyID, StateGeneration: v.StateGeneration, PlanScheduleRevision: v.PlanScheduleRevision, SeriesDigest: "series", Slot: 1}
}

type sampleRedis struct {
	redis.Cmdable
	mu      sync.Mutex
	lists   map[string][]string
	limits  map[string]int64
	ttls    map[string]time.Duration
	started chan struct{}
	release chan struct{}
	fail    bool
}
type samplePipeline struct {
	redis.Pipeliner
	client      *sampleRedis
	key, record string
	limit       int64
	ttl         time.Duration
}

func (c *sampleRedis) Pipeline() redis.Pipeliner { return &samplePipeline{client: c} }
func (p *samplePipeline) LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	p.key = key
	p.record = string(values[0].([]byte))
	return redis.NewIntResult(1, nil)
}
func (p *samplePipeline) LTrim(ctx context.Context, key string, start, stop int64) *redis.StatusCmd {
	p.limit = stop + 1
	return redis.NewStatusResult("OK", nil)
}
func (p *samplePipeline) Expire(ctx context.Context, key string, ttl time.Duration) *redis.BoolCmd {
	p.ttl = ttl
	return redis.NewBoolResult(true, nil)
}
func (p *samplePipeline) Exec(ctx context.Context) ([]redis.Cmder, error) {
	if p.client.started != nil {
		select {
		case p.client.started <- struct{}{}:
		default:
		}
	}
	if p.client.release != nil {
		select {
		case <-p.client.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.client.fail {
		return nil, errors.New("test write failure")
	}
	p.client.mu.Lock()
	defer p.client.mu.Unlock()
	p.client.lists[p.key] = append([]string{p.record}, p.client.lists[p.key]...)
	if len(p.client.lists[p.key]) > int(p.limit) {
		p.client.lists[p.key] = p.client.lists[p.key][:p.limit]
	}
	p.client.limits[p.key] = p.limit
	p.client.ttls[p.key] = p.ttl
	return nil, nil
}
func (c *sampleRedis) LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	records := c.lists[key]
	if len(records) > int(stop+1) {
		records = records[:stop+1]
	}
	return redis.NewStringSliceResult(append([]string(nil), records...), nil)
}
func newSampleRedis() *sampleRedis {
	return &sampleRedis{lists: map[string][]string{}, limits: map[string]int64{}, ttls: map[string]time.Duration{}}
}

func TestSeriesSampleStoreUsesIndependentRetentionAndSharedWriter(t *testing.T) {
	s, c := fleetSampleFixture(t)
	client := newSampleRedis()
	store, err := NewDiagnosticStore(client, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AttachSeriesSampler(s, 2); err != nil {
		t.Fatal(err)
	}
	r := s.TryReserve(context.Background(), c)
	r.AddLevel(1)
	r.Commit()
	queued := <-s.Records()
	store.writeOnce(context.Background(), diagnosticWrite{queryGroup: queued.QueryGroup(), record: queued.Bytes(), sample: true})
	queued.Release()
	store.writeOnce(context.Background(), diagnosticWrite{queryGroup: c.QueryGroup, record: []byte(`{"stage":"completion"}`)})
	records, err := store.LoadSeriesSamples(context.Background(), c.QueryGroup, 100)
	if err != nil || len(records) != 1 {
		t.Fatalf("load=%s %v", records, err)
	}
	if client.limits[store.sampleKey(c.QueryGroup)] != 2 || client.limits[store.key(c.QueryGroup)] != DiagnosticRecordsPerObject || client.ttls[store.sampleKey(c.QueryGroup)] != DiagnosticRetention {
		t.Fatal("samples share/evict lifecycle retention")
	}
	if health := store.Health(); health.Written != 1 || health.SampleWritten != 1 {
		t.Fatalf("health=%+v", health)
	}
}

func TestSeriesSampleSlowStoreDoesNotBackpressureReservation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, c := fleetSampleFixture(t)
		client := newSampleRedis()
		client.started = make(chan struct{}, 1)
		client.release = make(chan struct{})
		client.fail = fail
		store, _ := NewDiagnosticStore(client, "test")
		if err := store.AttachSeriesSampler(s, 2); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { store.Run(ctx); close(done) }()
		r := s.TryReserve(ctx, c)
		r.AddLevel(1)
		r.Commit()
		select {
		case <-client.started:
		case <-time.After(time.Second):
			t.Fatal("shared writer did not drain")
		}
		c.Slot++
		if got := testing.AllocsPerRun(1000, func() {
			if s.TryReserve(ctx, c) != nil {
				t.Fatal("in-flight buffer was released before store completion")
			}
		}); got != 0 {
			t.Fatalf("slow-store branch allocated %g", got)
		}
		// Cancellation interrupts the writer's actual bounded I/O context.
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("writer ignored cancellation")
		}
		if store.Health().SampleFailed != 1 {
			t.Fatalf("failed sample not counted: %+v", store.Health())
		}
		if next := s.TryReserve(context.Background(), c); next == nil {
			t.Fatal("write failure leaked reserved buffer")
		} else {
			next.Cancel()
		}
	}
}

func TestSeriesSampleWindowLifecycle(t *testing.T) {
	client := newFakeWindowRedis()
	windows, _ := NewWindowStore(client, "test")
	now := time.Now()
	q := digest("a")
	selection := observability.SeriesSampleSelection{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy", StateGeneration: "generation", PlanScheduleRevision: "schedule"}
	opened, err := windows.OpenSample(context.Background(), q, selection, "operator", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	first := SampleSelections(opened)
	if len(first) != 1 || first[0].WindowID == "" {
		t.Fatalf("open=%+v", opened)
	}
	s, c := fleetSampleFixture(t)
	if err := s.Select(first); err != nil {
		t.Fatal(err)
	}
	r := s.TryReserve(context.Background(), c)
	if r == nil {
		t.Fatal("valid window not selected")
	}
	r.AddLevel(1)
	r.Commit()
	closed, err := windows.Close(context.Background(), []string{q}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Select(SampleSelections(closed)); err != nil {
		t.Fatal(err)
	}
	c.Slot++
	if s.TryReserve(context.Background(), c) != nil {
		t.Fatal("closed window sampled")
	}
	old := <-s.Records()
	old.Release() // closing does not delete old queued evidence
	reopened, err := windows.OpenSample(context.Background(), q, selection, "operator", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if reopened[0].WindowID == opened[0].WindowID {
		t.Fatal("reopened window reused generation")
	}
	expired := SampleSelections(reopened)
	expired[0].OpenedAt = now.Add(-2 * time.Minute)
	expired[0].ExpiresAt = now.Add(-time.Minute)
	if err := s.Select(expired); err != nil {
		t.Fatal(err)
	}
	// No further control read or maintenance tick: local expiration suffices.
	if s.TryReserve(context.Background(), c) != nil || s.Health().Expired == 0 {
		t.Fatal("stale control snapshot kept sampling")
	}
}

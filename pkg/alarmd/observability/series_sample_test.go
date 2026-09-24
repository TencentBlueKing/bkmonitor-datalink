package observability

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleFixture(t testing.TB, capacity, records int) (*SeriesSampler, SeriesSampleSelection, SeriesSampleCandidate) {
	t.Helper()
	s, err := NewSeriesSampler(SeriesSampleLimits{RecordsPerMinute: records, BytesPerMinute: records * SeriesSampleMaxBytes, QueueCapacity: capacity})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	v := SeriesSampleSelection{QueryGroup: "query-group", WindowID: "window", OpenedAt: now, ExpiresAt: now.Add(time.Minute), TenantID: "tenant", BusinessID: "business", StrategyID: "strategy", StateGeneration: "generation", PlanScheduleRevision: "schedule"}
	if err := s.Select([]SeriesSampleSelection{v}); err != nil {
		t.Fatal(err)
	}
	c := SeriesSampleCandidate{QueryGroup: v.QueryGroup, TenantID: v.TenantID, BusinessID: v.BusinessID, StrategyID: v.StrategyID, StateGeneration: v.StateGeneration, PlanScheduleRevision: v.PlanScheduleRevision, SeriesDigest: "series", Slot: 1}
	return s, v, c
}

func TestSeriesSampleBudgetIsIndependentOfLifecycleAllocation(t *testing.T) {
	limits := SeriesSampleLimits{
		RecordsPerMinute: 2 * TargetFlowMaxRecords,
		BytesPerMinute:   2 * TargetFlowMaxBytes,
		QueueCapacity:    1,
	}
	sampler, err := NewSeriesSampler(limits)
	if err != nil {
		t.Fatalf("resource-derived sample allocation above lifecycle limits was rejected: %v", err)
	}
	if sampler.Limits() != limits {
		t.Fatalf("sample allocation changed: got %+v, want %+v", sampler.Limits(), limits)
	}
	limits.QueueCapacity = limits.RecordsPerMinute + 1
	if _, err := NewSeriesSampler(limits); err == nil {
		t.Fatal("queue capacity above the sample record budget was accepted")
	}
}

func TestSeriesSampleDisabledAllocatesNothing(t *testing.T) {
	s, v, c := sampleFixture(t, 1, 2)
	cases := map[string]struct {
		s *SeriesSampler
		c SeriesSampleCandidate
	}{"disabled": {nil, c}, "closed": {&SeriesSampler{}, c}}
	for _, field := range []string{"qg", "tenant", "business", "strategy", "generation", "schedule", "kind"} {
		other := c
		switch field {
		case "qg":
			other.QueryGroup = "sibling"
		case "tenant":
			other.TenantID = "sibling"
		case "business":
			other.BusinessID = "sibling"
		case "strategy":
			other.StrategyID = "sibling"
		case "generation":
			other.StateGeneration = "sibling"
		case "schedule":
			other.PlanScheduleRevision = "sibling"
		case "kind":
			other.SeriesKind = "NO_DATA"
		}
		cases[field] = struct {
			s *SeriesSampler
			c SeriesSampleCandidate
		}{s, other}
	}
	expired, _, _ := sampleFixture(t, 1, 2)
	expired.now = func() time.Time { return v.ExpiresAt.Add(time.Second) }
	cases["expired"] = struct {
		s *SeriesSampler
		c SeriesSampleCandidate
	}{expired, c}
	for name, arm := range cases {
		t.Run(name, func(t *testing.T) {
			if got := testing.AllocsPerRun(1000, func() {
				if r := arm.s.TryReserve(context.Background(), arm.c); r != nil {
					t.Fatal("unexpected reservation")
				}
			}); got != 0 {
				t.Fatalf("allocations=%g", got)
			}
		})
	}
}

func TestSeriesSampleReservationPrecedesMaterialization(t *testing.T) {
	for _, arm := range []struct {
		name              string
		capacity, records int
		wantBudget        bool
	}{{"queue", 1, 2, false}, {"records", 1, 1, true}, {"bytes", 2, 2, true}} {
		t.Run(arm.name, func(t *testing.T) {
			s, _, c := sampleFixture(t, arm.capacity, arm.records)
			if arm.name == "bytes" {
				s.limits.BytesPerMinute = SeriesSampleMaxBytes
			}
			r := s.TryReserve(context.Background(), c)
			if r == nil {
				t.Fatal("initial reservation")
			}
			c.Slot++
			if got := testing.AllocsPerRun(1000, func() {
				if s.TryReserve(context.Background(), c) != nil {
					t.Fatal("materialization admitted without capacity")
				}
			}); got != 0 {
				t.Fatalf("exhaustion allocated %g", got)
			}
			if arm.wantBudget && s.Health().BudgetDropped == 0 || !arm.wantBudget && s.Health().QueueDropped == 0 {
				t.Fatalf("health=%+v", s.Health())
			}
			r.Cancel()
			if next := s.TryReserve(context.Background(), c); next == nil {
				t.Fatal("cancel leaked reservation")
			} else {
				next.Cancel()
			}
		})
	}
}

func TestSeriesSamplePinsOneDigestAndPreservesWindowOnRefresh(t *testing.T) {
	s, v, c := sampleFixture(t, 1, 4)
	r := s.TryReserve(context.Background(), c)
	r.AddLevel(1)
	r.Commit()
	stored := <-s.Records()
	var sample SeriesSample
	if err := json.Unmarshal(stored.Bytes(), &sample); err != nil {
		t.Fatal(err)
	}
	if sample.Coverage != "first_seen_series_first_record" || !sample.Provisional {
		t.Fatalf("sample=%+v", sample)
	}
	stored.Release()
	if err := s.Select([]SeriesSampleSelection{v}); err != nil {
		t.Fatal(err)
	}
	if s.TryReserve(context.Background(), c) != nil {
		t.Fatal("refresh resampled same slot")
	}
	c.Slot++
	c.SeriesDigest = "other"
	if s.TryReserve(context.Background(), c) != nil {
		t.Fatal("discovery expanded to sibling series")
	}
	v.WindowID = "reopened"
	if err := s.Select([]SeriesSampleSelection{v}); err != nil {
		t.Fatal(err)
	}
	if r = s.TryReserve(context.Background(), c); r == nil {
		t.Fatal("new window retained old pin")
	} else {
		r.Cancel()
	}
}

func TestSeriesSampleConcurrentReservationAndClose(t *testing.T) {
	s, _, c := sampleFixture(t, 8, 32)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := s.TryReserve(context.Background(), c); r != nil {
				r.AddLevel(1)
				r.Commit()
			}
		}()
	}
	wg.Wait()
	if got := len(s.queue); got != 1 {
		t.Fatalf("same Slot admitted %d samples", got)
	}
	if err := s.Select(nil); err != nil {
		t.Fatal(err)
	}
	c.Slot++
	if s.TryReserve(context.Background(), c) != nil {
		t.Fatal("closed selection admitted sample")
	}
	(<-s.Records()).Release()
}

func TestSeriesSampleContentBounds(t *testing.T) {
	s, _, c := sampleFixture(t, 1, 4)
	r := s.TryReserve(context.Background(), c)
	r.AddLevel(1)
	r.Sample.EventID = strings.Repeat("x", 129)
	r.Commit()
	if len(s.queue) != 0 || s.Health().Oversize != 1 {
		t.Fatalf("unbounded content accepted: %+v", s.Health())
	}
	c.Slot++
	r = s.TryReserve(context.Background(), c)
	l := r.AddLevel(1)
	l.NormalizedScalar = "-0.125"
	l.ScalarStatus = "available"
	r.Commit()
	recorded := <-s.Records()
	if len(recorded.Bytes()) > SeriesSampleMaxBytes || !json.Valid(recorded.Bytes()) {
		t.Fatal("invalid bounded JSON")
	}
	recorded.Release()
	if SeriesSampleBufferBytes() < SeriesSampleMaxBytes {
		t.Fatal("memory accounting excludes payload")
	}
}

func worstCaseSampleStrings(value reflect.Value) {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.String {
			field.SetString(strings.Repeat("\x00", 128))
		}
		if field.Kind() == reflect.Slice {
			for j := 0; j < field.Len(); j++ {
				worstCaseSampleStrings(field.Index(j))
			}
		}
	}
}

func TestSeriesSampleEncodingScratchAndAttemptBudget(t *testing.T) {
	s, _, c := sampleFixture(t, 1, 1)
	r := s.TryReserve(context.Background(), c)
	r.AddLevel(1)
	r.AddLevel(2)
	worstCaseSampleStrings(reflect.ValueOf(&r.Sample).Elem())
	wire, err := json.Marshal(&r.Sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) > SeriesSampleEncodingScratchBytes/2 {
		t.Fatalf("schema outgrew its scratch bound: %d bytes", len(wire))
	}
	t.Logf("worst encoded whitelist=%d bytes; conservative concurrent slot=%d bytes", len(wire), SeriesSampleBufferBytes())
	r.Commit()
	if len(s.queue) != 0 || s.Health().Oversize != 1 {
		t.Fatal("oversize encoding was not dropped")
	}
	c.Slot++
	if s.TryReserve(context.Background(), c) != nil || s.Health().BudgetDropped != 1 {
		t.Fatal("encoding failure refunded already-spent CPU attempt budget")
	}
}

type sampleBlockedWriter struct {
	started chan struct{}
	release chan struct{}
}

func (w *sampleBlockedWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return len(p), nil
}

func TestSeriesSampleDoesNotUseBlockedTargetFlowLogger(t *testing.T) {
	s, v, c := sampleFixture(t, 1, 2)
	v.QueryGroup, c.QueryGroup = flowQG, flowQG
	if err := s.Select([]SeriesSampleSelection{v}); err != nil {
		t.Fatal(err)
	}
	w := &sampleBlockedWriter{started: make(chan struct{}), release: make(chan struct{})}
	flow, err := NewTargetFlow(New("runtime", w))
	if err != nil {
		t.Fatal(err)
	}
	if err := flow.Select([]string{v.QueryGroup}); err != nil {
		t.Fatal(err)
	}
	ctx := flow.Context(context.Background(), v.QueryGroup)
	logged := make(chan struct{})
	go func() { flow.Record("runner_decision", v.QueryGroup, TargetFlowFacts{}); close(logged) }()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("fixture did not block logger")
	}
	sampled := make(chan struct{})
	go func() { r := s.TryReserve(ctx, c); r.AddLevel(1); r.Commit(); close(sampled) }()
	select {
	case <-sampled:
	case <-time.After(time.Second):
		close(w.release)
		t.Fatal("sample waited on synchronous logger")
	}
	r := <-s.Records()
	var got SeriesSample
	if err := json.Unmarshal(r.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	r.Release()
	close(w.release)
	<-logged
	if got.ProcessID != flow.process || got.RunID == 0 {
		t.Fatalf("completion-chain linkage missing: %+v", got)
	}
}

func BenchmarkSeriesSample(b *testing.B) {
	for _, name := range []string{"disabled", "sibling_plan", "selected", "exhausted", "queue_full", "oversize"} {
		b.Run(name, func(b *testing.B) {
			s, _, c := sampleFixture(b, 1, 2)
			switch name {
			case "disabled":
				s = nil
			case "sibling_plan":
				c.StrategyID = "sibling"
			case "exhausted":
				s.records = s.limits.RecordsPerMinute
				s.start = time.Now()
			case "queue_full":
				held := s.TryReserve(context.Background(), c)
				defer held.Cancel()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Slot = int64(i + 2)
				if r := s.TryReserve(context.Background(), c); r != nil {
					r.AddLevel(1)
					if name == "oversize" {
						r.Sample.EventID = oversizeSampleField
					}
					r.Commit()
					if name != "oversize" {
						(<-s.Records()).Release()
					}
					s.mu.Lock()
					s.records = 0
					s.bytes = 0
					s.mu.Unlock()
				}
			}
		})
	}
}

var oversizeSampleField = strings.Repeat("x", 129)

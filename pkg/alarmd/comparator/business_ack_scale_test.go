package comparator

import (
	"context"
	"fmt"
	"testing"
)

type scaleAuditSink struct{ count int }

func (s *scaleAuditSink) WriteBusinessAudit(context.Context, *BusinessAudit) error {
	s.count++
	return nil
}

// Each run publishes every distinct pending Audit. Setup is excluded; lookup
// cost therefore grows with pending population rather than fixture construction.
func BenchmarkBusinessPublishACKScale(b *testing.B) {
	for _, n := range []int{512, 4096, 16384} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				r := &BusinessRun{entries: make(map[string]*businessEntry, n), partitions: map[BusinessPartition]*businessPartition{}, limits: BusinessLimits{MessageBytes: 1 << 20}}
				for j := 0; j < n; j++ {
					key := fmt.Sprint(j)
					r.entries[key] = &businessEntry{audit: &BusinessAudit{ID: key, Epoch: "epoch", Schema: "business-comparison-audit-v1", Differences: []string{}}}
				}
				sink := &scaleAuditSink{}
				b.StartTimer()
				err := r.PublishPending(context.Background(), sink)
				b.StopTimer()
				if err != nil || sink.count != n {
					b.Fatal(err, sink.count, n)
				}
				for _, e := range r.entries {
					if !e.acked {
						b.Fatal("ACK omitted")
					}
				}
			}
		})
	}
}

func TestBusinessPublishResidenceExpiresBeforeOffsetACK(t *testing.T) {
	e := &businessEntry{audit: &BusinessAudit{ID: "a", Epoch: "epoch", Schema: "business-comparison-audit-v1", Differences: []string{}}}
	r := &BusinessRun{epoch: "epoch", entries: map[string]*businessEntry{"a": e}, partitions: map[BusinessPartition]*businessPartition{}, limits: BusinessLimits{MessageBytes: 1 << 20}}
	checks := 0
	sink := &scaleAuditSink{}
	err := r.publishPending(context.Background(), sink, func() bool { checks++; return checks > 1 })
	if err != nil || sink.count != 2 || e.acked || !r.invalid || len(r.entries) != 0 {
		t.Fatal("expired in-flight Audit must close through gap, not offset ACK", err, sink.count, e.acked)
	}
}

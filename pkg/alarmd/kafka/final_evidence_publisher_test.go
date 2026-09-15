package kafka

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Public synthetic compiler->Go v2->official codec fixture. The PYTHON chain
// wrapper is for cross-language codec checks, not production Python evidence.
func finalPublisherFixture(t testing.TB) *contract.FinalResultEvidenceV1 {
	t.Helper()
	wire, err := os.ReadFile("testdata/final-evidence-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	record, err := contract.DecodeShadowResultRecordV1(wire, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return record.Evidence
}

func TestFinalPublisherSharesOutstandingQueueAndOwnsOfficialBytes(t *testing.T) {
	e := finalPublisherFixture(t)
	want, err := contract.EncodeFinalResultEvidenceV1(e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan *sarama.ProducerMessage, 1)
	release := make(chan error, 1)
	p, err := newReceiptPublisher("final-shadow", &fakeSyncProducer{send: func(m *sarama.ProducerMessage) (int32, int64, error) { started <- m; return 0, 1, <-release }}, &fakeCloser{}, ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(want)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := contract.EncodeImmutableFinalResultV1(e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	copy := encoded.CopyBytes()
	copy[0] = 'x'
	if !p.TryEnqueueEncodedFinalEvidence(encoded) {
		t.Fatal("final rejected")
	}
	m := <-started
	e.Primary.Values["value"] = "999"
	receipt := messageReceiptGolden(t)
	if p.TryEnqueue(&receipt) {
		t.Fatal("receipt bypassed in-flight final budget")
	}
	if p.TryEnqueueEncodedFinalEvidence(contract.EncodedFinalResultV1{}) {
		t.Fatal("zero encoded value accepted")
	}
	wire, err := m.Value.Encode()
	if err != nil || !bytes.Equal(wire, want) {
		t.Fatal("queued bytes changed with caller")
	}
	release <- nil
	r := p.Shutdown(context.Background())
	if r.Enqueued != 1 || r.Acked != 1 || r.PendingBytes != 0 || r.PendingMessages != 0 || r.Drops.QueueMessages != 1 || r.Drops.EncodeFailed != 1 {
		t.Fatalf("drain=%+v", r)
	}
}

func TestFinalPublisherRejectsEncodingAndBytesBeforeSend(t *testing.T) {
	e := finalPublisherFixture(t)
	wire, err := contract.EncodeFinalResultEvidenceV1(e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	p, err := newReceiptPublisher("final-shadow", &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { sends++; return 0, 1, nil }}, &fakeCloser{}, ReceiptPublisherLimits{MaxQueuedMessages: 2, MaxQueuedBytes: len(wire) - 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.TryEnqueueFinalEvidence(e, 1<<20) || p.TryEnqueueFinalEvidence(e, len(wire)-1) || p.TryEnqueueFinalEvidence(nil, 1<<20) {
		t.Fatal("invalid or overbudget final accepted")
	}
	r := p.Shutdown(context.Background())
	if sends != 0 || r.Drops.QueueBytes != 1 || r.Drops.EncodeFailed != 2 || r.PendingBytes != 0 {
		t.Fatalf("drain=%+v sends=%d", r, sends)
	}
}

func BenchmarkFinalEvidenceOfficialEncode(b *testing.B) {
	e := finalPublisherFixture(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := contract.EncodeFinalResultEvidenceV1(e, 1<<20); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFinalEvidenceEncodedCopy(b *testing.B) {
	e, err := contract.EncodeImmutableFinalResultV1(finalPublisherFixture(b), 1<<20)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(e.CopyBytes()) == 0 {
			b.Fatal("empty wire")
		}
	}
}

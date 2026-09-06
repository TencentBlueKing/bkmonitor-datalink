package kafka

import (
	"context"
	"errors"
	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"os"
	"testing"
)

func coveragePublisherFixture(t *testing.T) contract.EncodedGoCoverageV1 {
	raw, err := os.ReadFile("../contract/testdata/go-coverage-v1/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := contract.DecodeGoCoverageEnvelopeV1(raw, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := contract.EncodeGoCoverageEnvelopeV1(*e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}
func TestCoveragePublisherSharesInFlightAndKeepsTypedACK(t *testing.T) {
	wire := coveragePublisherFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan error, 1)
	finalACK := uint64(0)
	p, err := newReceiptPublisherWithDiagnostics("go-shadow", &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { started <- struct{}{}; return 0, 1, <-release }}, &fakeCloser{}, ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(wire.CopyBytes())}, ReceiptPublisherDiagnostics{OnACKed: func(n uint64) { finalACK += n }})
	if err != nil {
		t.Fatal(err)
	}
	if !p.TryEnqueueCoverageReceipt(wire) {
		t.Fatal("enqueue")
	}
	<-started
	snap := p.Snapshot()
	if snap.Coverage.Acked != 0 || snap.Coverage.PendingMessages != 1 || snap.PendingBytes != len(wire.CopyBytes()) {
		t.Fatal("inflight uncharged", snap)
	}
	if p.TryEnqueueCoverageReceipt(wire) {
		t.Fatal("bypassed shared count")
	}
	if p.TryEnqueueFinalEvidence(finalPublisherFixture(t), 1<<20) {
		t.Fatal("final bypassed receipt inflight")
	}
	if p.TryEnqueueCoverageReceipt(contract.EncodedGoCoverageV1{}) {
		t.Fatal("zero accepted")
	}
	release <- nil
	r := p.Shutdown(context.Background())
	if r.Coverage.Enqueued != 1 || r.Coverage.Acked != 1 || r.Coverage.Dropped != 2 || r.Coverage.PendingMessages != 0 || finalACK != 0 {
		t.Fatal("typed receipt ACK mistaken for final", r, finalACK)
	}
}
func TestCoveragePublisherByteAndBrokerFailure(t *testing.T) {
	wire := coveragePublisherFixture(t)
	for _, mode := range []string{"bytes", "broker"} {
		t.Run(mode, func(t *testing.T) {
			bound := len(wire.CopyBytes())
			if mode == "bytes" {
				bound--
			}
			p, err := newReceiptPublisher("go-shadow", &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, errors.New("broker failed") }}, &fakeCloser{}, ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: bound})
			if err != nil {
				t.Fatal(err)
			}
			accepted := p.TryEnqueueCoverageReceipt(wire)
			if accepted != (mode == "broker") {
				t.Fatal(accepted)
			}
			r := p.Shutdown(context.Background())
			if r.Coverage.Acked != 0 || r.Coverage.Dropped != 1 || r.PendingBytes != 0 {
				t.Fatal(r)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"os"
	"testing"
	"time"
)

type v3FiniteSource struct {
	messages chan *sarama.ConsumerMessage
	count    int64
}

func (s *v3FiniteSource) Partitions(string) ([]int32, error) { return []int32{0}, nil }
func (s *v3FiniteSource) GetOffset(_ string, _ int32, at int64) (int64, error) {
	if at == sarama.OffsetOldest {
		return 0, nil
	}
	return s.count, nil
}
func (s *v3FiniteSource) ConsumePartition(string, int32, int64) (enginekafka.FinitePartition, error) {
	return s, nil
}
func (s *v3FiniteSource) Messages() <-chan *sarama.ConsumerMessage { return s.messages }
func (s *v3FiniteSource) Errors() <-chan *sarama.ConsumerError     { return nil }
func (s *v3FiniteSource) Close() error                             { return nil }

type v3AuditSink struct {
	fail   bool
	audits []comparator.BusinessAudit
}

func (s *v3AuditSink) WriteBusinessAudit(_ context.Context, a *comparator.BusinessAudit) error {
	if s.fail {
		return errors.New("controlled Audit ACK failure")
	}
	s.audits = append(s.audits, *a)
	return nil
}

// The fixture is the original Python capture_to_stream output (actual native
// adapter + actual builder), not a reference manufactured from the Go result.
// Go records below are the bytes queued by the real production bundle above.
func assertV3RuntimeConsumer(t *testing.T, p *recordingFinalPublisher, m contract.ValidationEpochManifestV1) {
	t.Helper()
	assertV3RuntimeConsumerCase(t, p, m, false)
	if len(p.businessWire) == 0 {
		// An actual Go zero-anomaly terminal Receipt must expose an existing
		// Python business result as PYTHON_ONLY, not hide it behind FULL/empty.
		assertV3RuntimeConsumerCase(t, p, m, true)
	}
}
func assertV3RuntimeConsumerCase(t *testing.T, p *recordingFinalPublisher, m contract.ValidationEpochManifestV1, pythonOnly bool) {
	t.Helper()
	prefix := ""
	pythonEnd := int64(1)
	if len(p.businessWire) == 0 && !pythonOnly {
		prefix = "empty-"
		pythonEnd = 0
	}
	read := func(name string) []byte {
		b, e := os.ReadFile("../../contract/testdata/business-query-v3/" + name)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	pythonManifest := read(prefix + "manifest.json")
	pythonSummary := read(prefix + "summary.json")
	var summaryFields struct {
		Snapshots string `json:"snapshots_sha256"`
	}
	if err := json.Unmarshal(pythonSummary, &summaryFields); err != nil {
		t.Fatal(err)
	}
	python := comparator.BusinessPartition{Chain: contract.ShadowPython, Topic: "python-native", Partition: 0}
	goPart := comparator.BusinessPartition{Chain: contract.ShadowGo, Topic: m.GoTopic.Name, Partition: 0}
	n := int64(len(p.published))
	header := comparator.BusinessCaptureHeader{Manifest: m, PythonSourceManifest: pythonManifest, PythonSnapshotsSHA256: summaryFields.Snapshots,
		Ranges: []comparator.BusinessRange{{BusinessPartition: python, Start: 0, End: pythonEnd}, {BusinessPartition: goPart, Start: 0, End: n}},
		Limits: comparator.BusinessLimits{Entries: 100, Bytes: 1 << 20, MessageBytes: 256 << 10, OffsetEntries: 100, MaxAge: 10 * time.Minute}, GraceMillis: 1000}
	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	if err := enc.Encode(header); err != nil {
		t.Fatal(err)
	}
	if pythonEnd > 0 {
		input.Write(read("capture.jsonl"))
	}
	ranges := []enginekafka.FiniteRange{{Topic: m.GoTopic.Name, Partition: 0, Start: 0, End: n}}
	if err := enc.Encode(map[string]any{"schema": "go-finite-capture-v1", "ranges": ranges, "max_records": 100, "max_bytes": 1 << 20, "max_partitions": 1, "timeout_millis": 1000}); err != nil {
		t.Fatal(err)
	}
	source := &v3FiniteSource{messages: make(chan *sarama.ConsumerMessage, len(p.published)), count: n}
	for i, wire := range p.published {
		source.messages <- &sarama.ConsumerMessage{Topic: m.GoTopic.Name, Partition: 0, Offset: int64(i), Value: wire}
	}
	result, err := enginekafka.ReadFinite(context.Background(), source, ranges, enginekafka.FiniteLimits{MaxRecords: 100, MaxBytes: 1 << 20, MaxPartitions: 1, Timeout: time.Second}, func(r enginekafka.FiniteRecord) error {
		if r.Kind != enginekafka.FiniteBusiness && r.Kind != enginekafka.FiniteReceipt {
			t.Fatalf("actual version not recognized: %s", r.Kind)
		}
		return enc.Encode(r)
	})
	if err != nil || !result.ReferenceComplete {
		t.Fatalf("actual finite read: %+v %v", result, err)
	}
	if err = enc.Encode(map[string]any{"schema": "go-finite-capture-summary-v1", "summary": result}); err != nil {
		t.Fatal(err)
	}
	if err = enc.Encode(comparator.BusinessCaptureFrame{Kind: "CLOSE", ObservedAt: 4102444863000, PythonSummary: pythonSummary}); err != nil {
		t.Fatal(err)
	}
	for _, failed := range []bool{true, false} {
		sink := &v3AuditSink{fail: failed}
		outcome, offsets, err := comparator.RunBusinessCapture(context.Background(), bytes.NewReader(input.Bytes()), sink)
		if failed {
			if err == nil || outcome != "PENDING_AUDIT" || len(offsets) != 0 {
				t.Fatalf("Audit failure crossed offset barrier: %s %+v %v", outcome, offsets, err)
			}
			continue
		}
		wantOutcome := "MATCHED_SAME"
		wantVerdict := "MATCHED_SAME"
		if pythonOnly {
			wantOutcome = "FAILED"
			wantVerdict = "PYTHON_ONLY"
		}
		if pythonEnd == 0 {
			wantOutcome = "NO_ABNORMAL_NOT_EPOCH_PASS"
		}
		if err != nil || outcome != wantOutcome || offsets[goPart] != n || offsets[python] != pythonEnd {
			t.Fatalf("actual v3 comparison: %s %+v %v audits=%+v", outcome, offsets, err, sink.audits)
		}
		points := 0
		for _, audit := range sink.audits {
			if audit.SubjectKind == "POINT" {
				points++
				if audit.Verdict != wantVerdict {
					t.Fatalf("actual reference mismatch: %+v", audit)
				}
			}
		}
		if points != int(pythonEnd) {
			t.Fatalf("points=%d expected=%d", points, pythonEnd)
		}
	}
}

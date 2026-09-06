package comparator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestBusinessExecutableCaptureRequiresReceiptRangeAndAuditACK(t *testing.T) {
	ref, config, receipt := businessReceiptFixture(t, false)
	p := BusinessPartition{contract.ShadowPython, "native", 0}
	g := BusinessPartition{contract.ShadowGo, "shadow", 0}
	d := strings.Repeat("a", 64)
	m := contract.ValidationEpochManifestV1{Schema: contract.Schema{Name: contract.ValidationEpochManifestSchemaV1, Major: 1}, RequiredFeatures: []string{}, EpochID: "epoch", StartedAt: 100, EligibleFrom: 100, ExpectedEnd: 300,
		Target: contract.ShadowTargetScopeV1{TenantID: ref.Subject.TenantID, BusinessID: ref.Subject.BusinessID, DataType: "SERIES", Capabilities: []string{"Threshold"}, ExcludedCapabilities: []string{}},
		Python: contract.ShadowSourceVersionV1{Commit: strings.Repeat("a", 40), Image: "python", Schema: "python-final-v1"}, Go: contract.ShadowSourceVersionV1{Commit: strings.Repeat("b", 40), Image: "go", Schema: "go-final-v1"}, StrategyObservation: "observation", StrategyPublication: "publication", ComparisonVersion: "python-business-kafka-v1", IdentityVersion: "identity-v1",
		PythonTopic: contract.ShadowTopicV1{Name: "python-shadow", ConsumerGroup: "comparison", Partitions: 1}, GoTopic: contract.ShadowTopicV1{Name: "shadow", ConsumerGroup: "comparison", Partitions: 1}, AuditTopic: contract.ShadowTopicV1{Name: "audit-shadow", ConsumerGroup: "audit", Partitions: 1},
		Limits: contract.ShadowResourceLimitsV1{MaxEntries: 10, MaxRetainedBytes: 1 << 20, MaxAgeSeconds: 3600, MaxQueueEntries: 10, MaxQueueBytes: 1 << 20, MaxAuditInflight: 1, MaxMessageBytes: 1 << 18}, GracePolicyRevision: "grace-v1", RuntimeConfigDigests: []string{d}, KnownExclusionReasons: []string{}}
	if err := contract.ValidateValidationEpochManifestV1(&m); err != nil {
		t.Fatal(err)
	}
	header := BusinessCaptureHeader{Manifest: m, Ranges: []BusinessRange{{p, 0, 1}, {g, 0, 0}}, Limits: BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour}, GraceMillis: 1000, PythonSnapshotsSHA256: d, PythonSourceManifest: json.RawMessage(`{"topic":"native","partitions":[{"partition":0,"start":0,"end":1}],"strategy_ids":[1001]}`)}
	for _, variant := range []string{"complete", "missing_receipt", "range_gap", "audit_failure", "malformed", "bad_hash", "capacity", "missing_summary", "resident_expired", "archive_time"} {
		t.Run(variant, func(t *testing.T) {
			var input bytes.Buffer
			enc := json.NewEncoder(&input)
			if variant == "capacity" {
				header.Limits.Bytes = 1
			}
			_ = enc.Encode(header)
			header.Limits.Bytes = 1 << 20
			raw, readErr := os.ReadFile("../contract/testdata/business-kafka-v1/native.json")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if variant == "malformed" {
				input.WriteString("{bad\n")
			}
			rawHash := sha256.Sum256(raw)
			if variant == "bad_hash" {
				rawHash = [32]byte{}
			}
			_ = enc.Encode(pythonCaptureRecord{Topic: p.Topic, Partition: 0, Offset: 0, RawSHA256: hex.EncodeToString(rawHash[:]), RawBase64: base64.StdEncoding.EncodeToString(raw), Classification: "REFERENCE", Reference: businessFixture(t)})
			if variant != "missing_receipt" {
				_ = enc.Encode(BusinessCaptureFrame{Kind: "GO_RECEIPT", Subject: ref.Subject, Receipt: receipt, Config: config, CompletedAt: 201000})
			}
			proof := &BusinessPythonCompletion{Topic: "native", Complete: true, ReferenceComplete: true, FinishedAt: 202000, SummarySHA256: d, Partitions: []BusinessPythonPartition{{Partition: 0, Start: 0, End: 1, Low: 0, High: 1, ReadEnd: 1}}}
			if variant == "range_gap" {
				proof.Partitions[0].ReadEnd = 0
			}
			sourceWire, _ := contract.CanonicalJSONV2(header.PythonSourceManifest)
			manifestHash := sha256.Sum256(sourceWire)
			summary, _ := json.Marshal(map[string]any{"schema": "python-business-kafka-range-v1", "topic": proof.Topic, "complete": proof.Complete, "reference_complete": proof.ReferenceComplete, "finished_at": float64(proof.FinishedAt) / 1000, "manifest_sha256": hex.EncodeToString(manifestHash[:]), "snapshots_sha256": header.PythonSnapshotsSHA256, "partitions": proof.Partitions, "classification_counts": map[string]int{"REFERENCE": 1}})
			if variant == "missing_summary" {
				summary = nil
			}
			_ = enc.Encode(BusinessCaptureFrame{Kind: "CLOSE", ObservedAt: 203000, PythonSummary: summary})
			sink := &businessSink{fail: variant == "audit_failure"}
			clockCalls := 0
			now := func() time.Time {
				clockCalls++
				if variant == "resident_expired" {
					return time.Unix(100000, 0).Add(time.Duration(clockCalls) * time.Hour)
				}
				return time.Unix(100000, 0)
			}
			status, offsets, err := runBusinessCaptureWithClock(context.Background(), &input, sink, now)
			switch variant {
			case "complete", "archive_time":
				if err != nil || status != "FAILED" || offsets[p] != 1 || len(sink.audits) != 1 || sink.audits[0].Verdict != "PYTHON_ONLY" {
					t.Fatal(status, offsets, err, sink.audits)
				}
			case "missing_receipt":
				if err != nil || status != "PENDING" || offsets[p] != 0 || len(sink.audits) != 0 {
					t.Fatal("missing Receipt became terminal", status, err)
				}
			case "resident_expired", "range_gap", "malformed", "bad_hash", "capacity", "missing_summary":
				if err == nil || len(sink.audits) != 1 || sink.audits[0].SubjectKind != "EPOCH_GAP" {
					t.Fatal("range gap accepted")
				}
			case "audit_failure":
				if err == nil || status != "PENDING_AUDIT" || offsets != nil {
					t.Fatal("Audit failure permitted offsets")
				}
			}
		})
	}
}

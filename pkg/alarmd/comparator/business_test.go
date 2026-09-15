package comparator

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func businessFixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../contract/testdata/business-kafka-v1/reference.json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func businessGoWire(t *testing.T, r *contract.BusinessAbnormalV1) []byte {
	t.Helper()
	d := strings.Repeat("a", 64)
	ctx := contract.ShadowContextV1{ComparisonConfigDigest: d, PlanScheduleRevision: "plan", EvaluationTime: 180, SlotIdentity: "slot", SnapshotRevision: "snapshot", QueryRevision: "query", QueryGroupScheduleRevision: "schedule", DuePlanSetDigest: d, EffectiveTimeRequirementDigest: d, EffectiveTimeFactDigest: d}
	wire, err := contract.EncodeGoBusinessAbnormalV1(contract.GoBusinessAbnormalV1{Schema: "go-business-abnormal-v1", RecordType: "BUSINESS_ABNORMAL", EpochID: "epoch", Context: ctx, Reference: *r}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return wire.CopyBytes()
}

type businessSink struct {
	fail   bool
	audits []BusinessAudit
}

func (s *businessSink) WriteBusinessAudit(_ context.Context, a *BusinessAudit) error {
	if s.fail {
		return errors.New("broker failed")
	}
	s.audits = append(s.audits, *a)
	return nil
}

func TestBusinessVerdictsGraceAndAuditBarrier(t *testing.T) {
	for _, variant := range []string{"same", "diff", "python_only", "go_only", "unknown_config"} {
		t.Run(variant, func(t *testing.T) {
			python := BusinessPartition{contract.ShadowPython, "native", 0}
			goPart := BusinessPartition{contract.ShadowGo, "shadow", 0}
			pe, ge := int64(1), int64(1)
			if variant == "python_only" {
				ge = 0
			}
			if variant == "go_only" {
				pe = 0
			}
			r, err := NewBusinessRun("epoch", []BusinessRange{{python, 0, pe}, {goPart, 0, ge}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			wire := businessFixture(t)
			ref, err := contract.DecodeBusinessAbnormalV1(wire, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Unix(200, 0)
			if pe > 0 {
				if err = r.Observe(at, BusinessOffset{python, 0, "raw-python"}, wire); err != nil {
					t.Fatal(err)
				}
			}
			if ge > 0 {
				ref.InputQuality = "FULL"
				ref.Native.EventID = "go-event"
				if variant == "diff" {
					ref.Primary.Values["value"] = "82"
				}
				ref.SemanticDigest, _ = contract.BusinessSemanticDigestV1(*ref)
				encoded := businessGoWire(t, ref)
				var err error
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Observe(at, BusinessOffset{goPart, 0, "raw-go"}, encoded); err != nil {
					t.Fatal(err)
				}
			}
			if audits, err := r.Finalize(at.Add(time.Hour), at, time.Second); err != nil || len(audits) != 0 {
				t.Fatal("missing Receipt inferred from grace")
			}
			// Core state fixture: the separate receipt adapter owns production proof.
			for _, e := range r.entries {
				e.goClosed = true
				e.goConfig = ref.ConfigDigest
				e.goCompleted = at
				e.receiptDigest = "receipt"
				if variant == "unknown_config" {
					e.goConfig = "different"
				}
			}
			if audits, err := r.Finalize(at, at, time.Second); err != nil || len(audits) != 0 {
				t.Fatal("premature missing")
			}
			audits, err := r.Finalize(at.Add(time.Second), at, time.Second)
			if err != nil || len(audits) != 1 {
				t.Fatal(audits, err)
			}
			want := map[string]string{"same": "MATCHED_SAME", "diff": "MATCHED_DIFF", "python_only": "PYTHON_ONLY", "go_only": "GO_ONLY", "unknown_config": ""}[variant]
			if audits[0].Verdict != want {
				t.Fatal(audits[0])
			}
			sink := &businessSink{fail: true}
			if err = r.PublishPending(context.Background(), sink); err == nil {
				t.Fatal("Audit failure ignored")
			}
			if offset, _ := r.Committable(python); offset != 0 {
				t.Fatal("offset before Audit ACK")
			}
			sink.fail = false
			if err = r.PublishPending(context.Background(), sink); err != nil {
				t.Fatal(err)
			}
			if offset, _ := r.Committable(python); offset != pe {
				t.Fatal("offset after ACK", offset)
			}
			if variant == "python_only" || variant == "diff" {
				if r.Result() != "FAILED" {
					t.Fatal(r.Result())
				}
			}
			if variant == "go_only" && r.Result() != "GO_ONLY_REVIEW_REQUIRED" {
				t.Fatal("symmetric migration failure")
			}
		})
	}
}

func TestBusinessCapacityGapAndContiguousSkip(t *testing.T) {
	p := BusinessPartition{contract.ShadowPython, "native", 0}
	g := BusinessPartition{contract.ShadowGo, "shadow", 0}
	r, err := NewBusinessRun("epoch", []BusinessRange{{p, 0, 0}, {g, 0, 2}}, BusinessLimits{1, 1 << 20, 1 << 18, 3, time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := contract.DecodeBusinessAbnormalV1(businessFixture(t), 1<<20)
	if err = r.Observe(time.Unix(1, 0), BusinessOffset{g, 0, "raw"}, businessGoWire(t, ref)); err != nil {
		t.Fatal(err)
	}
	if err = r.SkipNative(BusinessOffset{g, 1, "raw-native"}); err != nil {
		t.Fatal(err)
	}
	if offset, _ := r.Committable(g); offset != 0 {
		t.Fatal("native skipped pending evidence Audit")
	}
	if err = r.Expire(time.Unix(3, 0)); err != nil {
		t.Fatal(err)
	}
	sink := &businessSink{fail: true}
	_ = r.PublishPending(context.Background(), sink)
	if offset, _ := r.Committable(g); offset != 0 || len(r.entries) != 1 {
		t.Fatal("release before gap ACK")
	}
	sink.fail = false
	if err = r.PublishPending(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if offset, _ := r.Committable(g); offset != 2 || len(r.entries) != 0 || r.Result() != "UNPROVEN" {
		t.Fatal("gap release or Epoch disposition")
	}
}

func TestBusinessNormalIsCoarseAndRecoveryNotInDenominator(t *testing.T) {
	if CompareStrategyNormal(StrategyNormal{}, true, 0) != "UNPROVEN" {
		t.Fatal("missing log became NORMAL")
	}
	p := StrategyNormal{"1001", "trace", 100, 0, true}
	if CompareStrategyNormal(p, true, 0) != "STRATEGY_NORMAL_COARSE_AGREEMENT" {
		t.Fatal("normal")
	}
	if CompareStrategyNormal(p, true, 1) != "GO_ONLY_REVIEW_REQUIRED" {
		t.Fatal("Go abnormal discarded")
	}
}

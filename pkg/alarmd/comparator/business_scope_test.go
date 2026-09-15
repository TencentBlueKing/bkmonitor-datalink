package comparator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"strings"
	"testing"
	"time"
)

func scopeWire(t *testing.T, produced bool) []byte {
	t.Helper()
	_, cfg, receipt := businessReceiptFixture(t, produced)
	at := int64(201000)
	encoded, err := contract.EncodeGoCoverageEnvelopeV1(contract.GoCoverageEnvelopeV1{EpochID: "epoch", Receipt: *receipt, Config: cfg, CompletedAt: &at}, 1<<18)
	if err != nil {
		t.Fatal(err)
	}
	return encoded.CopyBytes()
}
func TestBusinessScopeFirstAndZeroResultWaitForCoverageAudit(t *testing.T) {
	for _, hasPython := range []bool{false, true} {
		p := BusinessPartition{contract.ShadowPython, "native", 0}
		g := BusinessPartition{contract.ShadowGo, "shadow", 0}
		end := int64(0)
		if hasPython {
			end = 1
		}
		r, err := NewBusinessRun("epoch", []BusinessRange{{p, 0, end}, {g, 0, 1}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if err = r.ObserveGoCoverage(time.Unix(200, 0), BusinessOffset{g, 0, "raw"}, scopeWire(t, false)); err != nil {
			t.Fatal(err)
		}
		if hasPython {
			if err = r.Observe(time.Unix(200, 0), BusinessOffset{p, 0, "raw"}, businessFixture(t)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = r.Finalize(time.Unix(203, 0), time.Unix(202, 0), time.Second); err != nil {
			t.Fatal(err)
		}
		if got, _ := r.Committable(g); got != 0 {
			t.Fatal("Receipt advanced before Audit")
		}
		sink := &businessSink{fail: true}
		if err = r.PublishPending(context.Background(), sink); err == nil {
			t.Fatal("expected ACK failure")
		}
		if got, _ := r.Committable(g); got != 0 {
			t.Fatal("failed Audit advanced Receipt")
		}
		sink.fail = false
		if err = r.PublishPending(context.Background(), sink); err != nil {
			t.Fatal(err)
		}
		if got, _ := r.Committable(g); got != 1 {
			t.Fatal("Receipt did not advance after all Audits")
		}
		if len(sink.audits) != int(end)+1 {
			t.Fatalf("audits=%d", len(sink.audits))
		}
	}
}

type scopeSelectiveSink struct {
	failCoverage bool
	audits       []BusinessAudit
}

func (s *scopeSelectiveSink) WriteBusinessAudit(_ context.Context, a *BusinessAudit) error {
	if s.failCoverage && a.SubjectKind == "COVERAGE" {
		return errors.New("coverage ACK failed")
	}
	s.audits = append(s.audits, *a)
	return nil
}
func TestBusinessScopeMultipleSubjectsAndCoverageACKBarrier(t *testing.T) {
	p := BusinessPartition{contract.ShadowPython, "native", 0}
	g := BusinessPartition{contract.ShadowGo, "shadow", 0}
	r, _ := NewBusinessRun("epoch", []BusinessRange{{p, 0, 2}, {g, 0, 1}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour})
	if err := r.ObserveGoCoverage(time.Unix(200, 0), BusinessOffset{g, 0, "raw"}, scopeWire(t, false)); err != nil {
		t.Fatal(err)
	}
	for i := int64(0); i < 2; i++ {
		ref, err := contract.DecodeBusinessAbnormalV1(businessFixture(t), 1<<18)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			ref.Subject.DimensionIdentityDigest = strings.Repeat("b", 64)
		}
		ref.SemanticDigest, err = contract.BusinessSemanticDigestV1(*ref)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := contract.EncodeBusinessAbnormalV1(ref, 1<<18)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Observe(time.Unix(200, 0), BusinessOffset{p, i, "raw"}, wire.CopyBytes()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Finalize(time.Unix(203, 0), time.Unix(202, 0), time.Second); err != nil {
		t.Fatal(err)
	}
	sink := &scopeSelectiveSink{failCoverage: true}
	if err := r.PublishPending(context.Background(), sink); err == nil {
		t.Fatal("Coverage ACK should fail")
	}
	if len(sink.audits) != 2 {
		t.Fatal("both POINT Audits required")
	}
	if got, _ := r.Committable(g); got != 0 {
		t.Fatal("Receipt passed missing Coverage ACK")
	}
	sink.failCoverage = false
	if err := r.PublishPending(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Committable(g); got != 1 {
		t.Fatal("barrier stuck")
	}
	if len(sink.audits) != 3 {
		t.Fatal("POINT re-published unexpectedly")
	}
}
func TestBusinessScopeReplayConflictBoundsAndAge(t *testing.T) {
	p := BusinessPartition{contract.ShadowPython, "native", 0}
	g := BusinessPartition{contract.ShadowGo, "shadow", 0}
	for _, shape := range []string{"duplicate", "conflict", "capacity", "age"} {
		t.Run(shape, func(t *testing.T) {
			r, _ := NewBusinessRun("epoch", []BusinessRange{{p, 0, 0}, {g, 0, 2}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Second})
			wire := scopeWire(t, false)
			if err := r.ObserveGoCoverage(time.Unix(200, 0), BusinessOffset{g, 0, "raw"}, wire); err != nil {
				t.Fatal(err)
			}
			before := r.bytes
			if shape == "age" {
				if err := r.Expire(time.Unix(202, 0)); err != nil || r.gap == nil {
					t.Fatal("scope-only age not bounded")
				}
				return
			}
			if shape == "conflict" {
				e, _ := contract.DecodeGoCoverageEnvelopeV1(wire, 1<<18)
				*e.CompletedAt++
				encoded, err := contract.EncodeGoCoverageEnvelopeV1(*e, 1<<18)
				if err != nil {
					t.Fatal(err)
				}
				wire = encoded.CopyBytes()
			}
			if shape == "capacity" {
				r.limits.Bytes = r.bytes
			}
			err := r.ObserveGoCoverage(time.Unix(200, 0), BusinessOffset{g, 1, "raw"}, wire)
			if shape == "duplicate" {
				if err != nil || len(r.scopes) != 1 || r.bytes != before+128 {
					t.Fatal("bounded duplicate", err)
				}
			} else {
				if err == nil || r.bytes != before || r.partitions[g].read != 1 {
					t.Fatal("rejected receipt mutated ownership", err)
				}
			}
		})
	}
}
func TestBusinessNonterminalNeverOverridesTerminal(t *testing.T) {
	p := BusinessPartition{contract.ShadowPython, "native", 0}
	g := BusinessPartition{contract.ShadowGo, "shadow", 0}
	terminal := scopeWire(t, false)
	e, _ := contract.DecodeGoCoverageEnvelopeV1(terminal, 1<<18)
	e.Receipt.CoverageComplete = false
	e.Receipt.TerminalFact = false
	e.Receipt.TerminalFactRef = ""
	e.CompletedAt = nil
	e.Receipt.GapReasons = []string{"OBSERVATION_ONLY"}
	e.Receipt.Input.QueryAttempts.LogicalSlot = contract.KnownCountV1{}
	encoded, err := contract.EncodeGoCoverageEnvelopeV1(*e, 1<<18)
	if err != nil {
		t.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		r, _ := NewBusinessRun("epoch", []BusinessRange{{p, 0, 1}, {g, 0, 2}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour})
		wires := [][]byte{encoded.CopyBytes(), terminal}
		if reverse {
			wires[0], wires[1] = wires[1], wires[0]
		}
		for i, wire := range wires {
			if err = r.ObserveGoCoverage(time.Unix(200, 0), BusinessOffset{g, int64(i), "raw"}, wire); err != nil {
				t.Fatal(err)
			}
		}
		if err = r.Observe(time.Unix(200, 0), BusinessOffset{p, 0, "raw"}, businessFixture(t)); err != nil {
			t.Fatal(err)
		}
		audits, err := r.Finalize(time.Unix(203, 0), time.Unix(202, 0), time.Second)
		if err != nil || len(audits) != 1 || audits[0].Verdict != "PYTHON_ONLY" {
			t.Fatal("attempt displaced real terminal", err, audits)
		}
	}
}

func captureRow(t *testing.T, offset int64, raw []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(raw)
	wire, err := json.Marshal(goCaptureRecord{Topic: "shadow", Offset: offset, Value: raw, ValueSHA256: hex.EncodeToString(sum[:]), ObservedAt: time.Unix(200, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}
func TestBusinessMixedRawTopicExclusionDoesNotCrossPendingReceipt(t *testing.T) {
	p := BusinessPartition{contract.ShadowPython, "native", 0}
	g := BusinessPartition{contract.ShadowGo, "shadow", 0}
	r, _ := NewBusinessRun("epoch", []BusinessRange{{p, 0, 0}, {g, 0, 3}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour})
	e, _ := contract.DecodeGoCoverageEnvelopeV1(scopeWire(t, false), 1<<18)
	r.target = &contract.ShadowTargetScopeV1{TenantID: e.Receipt.TenantID, BusinessID: e.Receipt.BusinessID}
	r.pythonStrategies = map[string]bool{e.Receipt.StrategyID: true}
	if err := r.observeGoCapture(time.Unix(300, 0), captureRow(t, 0, scopeWire(t, false))); err != nil {
		t.Fatal(err)
	}
	e.Receipt.StrategyID = "other"
	encoded, err := contract.EncodeGoCoverageEnvelopeV1(*e, 1<<18)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.observeGoCapture(time.Unix(300, 0), captureRow(t, 1, encoded.CopyBytes())); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Committable(g); got != 0 {
		t.Fatal("excluded record crossed pending Receipt")
	}
	if err = r.observeGoCapture(time.Unix(300, 0), captureRow(t, 2, []byte("{bad"))); err == nil {
		t.Fatal("bad JSON classified out of scope")
	}
	c := r.goCaptureCounts
	if c.Seen != 3 || c.TargetCoverage != 1 || c.OutOfScope != 1 || c.Invalid != 1 {
		t.Fatalf("counts=%+v", c)
	}
	_ = r.Gap("EVIDENCE_GAP")
	sink := &businessSink{fail: true}
	if err = r.PublishPending(context.Background(), sink); err == nil {
		t.Fatal("gap ACK expected to fail")
	}
	if got, _ := r.Committable(g); got != 0 {
		t.Fatal("failed gap Audit crossed barrier")
	}
	sink.fail = false
	if err = r.PublishPending(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Committable(g); got != 2 {
		t.Fatal("gap acknowledged unread malformed offset")
	}
	if sink.audits[0].CaptureCounts == nil || sink.audits[0].CaptureCounts.OutOfScope != 1 {
		t.Fatal("exclusion facts missing from Audit")
	}
}
func TestBusinessScopeFrozenObservationReplayStable(t *testing.T) {
	var want []byte
	for _, now := range []int64{300, 900} {
		p := BusinessPartition{contract.ShadowPython, "native", 0}
		g := BusinessPartition{contract.ShadowGo, "shadow", 0}
		r, _ := NewBusinessRun("epoch", []BusinessRange{{p, 0, 0}, {g, 0, 1}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour})
		if err := r.observeGoCapture(time.Unix(now, 0), captureRow(t, 0, scopeWire(t, false))); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Finalize(time.Unix(now, 0), time.Unix(202, 0), time.Second); err != nil {
			t.Fatal(err)
		}
		sink := &businessSink{}
		if err := r.PublishPending(context.Background(), sink); err != nil {
			t.Fatal(err)
		}
		if len(sink.audits) != 1 || sink.audits[0].FirstSeen != 200000 || sink.audits[0].Completed != 202000 || sink.audits[0].GraceDeadline != 203000 {
			t.Fatal("local replay clock entered Audit", sink.audits)
		}
		got, err := EncodeBusinessAudit(&sink.audits[0], 1<<18)
		if err != nil {
			t.Fatal(err)
		}
		if want == nil {
			want = got
		} else if string(want) != string(got) {
			t.Fatal("same archive changed Audit bytes")
		}
	}
}

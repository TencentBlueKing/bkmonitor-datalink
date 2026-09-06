package comparator

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const coverageOffsetPrefix = "coverage:"

type businessScope struct {
	envelope      *contract.GoCoverageRecord
	first         time.Time
	auditFirst    int64
	auditFirstSet bool
	locator       BusinessOffset
	audit         *BusinessAudit
	acked         bool
}

// ObserveGoCoverage retains one validated Plan/Slot scope under the existing
// entry/byte/age bounds. It never invents a dimension identity or terminal time.
func (r *BusinessRun) ObserveGoCoverage(at time.Time, offset BusinessOffset, wire []byte) error {
	if r.gap != nil {
		return errors.New("business Epoch closed by gap")
	}
	p, ok := r.partitions[offset.BusinessPartition]
	if !ok || offset.Chain != contract.ShadowGo || offset.Offset != p.read || offset.Offset >= p.end {
		return errors.New("coverage offset")
	}
	if r.offsetCount() >= r.limits.OffsetEntries {
		return ErrBusinessBackpressure
	}
	e, err := contract.DecodeGoCoverageRecord(wire, r.limits.MessageBytes)
	if err != nil {
		return err
	}
	if e.EpochID != r.epoch {
		return errors.New("coverage Epoch mismatch")
	}
	if r.target != nil && (e.Receipt.TenantID != r.target.TenantID || e.Receipt.BusinessID != r.target.BusinessID || !r.pythonStrategies[e.Receipt.StrategyID]) {
		return errors.New("coverage outside frozen target")
	}
	old := r.scopes[e.Identity]
	if old != nil && old.envelope.Digest != e.Digest {
		return errors.New("coverage identity payload conflict")
	}
	if old != nil && old.audit != nil {
		return errors.New("coverage after terminal")
	}
	cost := 128
	if old == nil {
		if len(r.entries)+len(r.scopes) >= r.limits.Entries {
			return ErrBusinessBackpressure
		}
		cost += len(wire)
	}
	if r.bytes > r.limits.Bytes-cost {
		return ErrBusinessBackpressure
	}
	if old == nil {
		r.scopes[e.Identity] = &businessScope{envelope: e, first: at, auditFirst: at.UnixMilli(), locator: offset}
	}
	r.bytes += cost
	p.pending[offset.Offset] = coverageOffsetPrefix + e.Identity
	p.read++
	return nil
}
func scopeMatches(e *contract.GoCoverageRecord, subject contract.ShadowSubjectV1) bool {
	receipt := e.Receipt
	return subject.TenantID == receipt.TenantID && subject.BusinessID == receipt.BusinessID && subject.StrategyID == receipt.StrategyID && subject.SourceTime >= receipt.Input.SourceWindow.FromTime && subject.SourceTime < receipt.Input.SourceWindow.UntilTime
}
func (r *BusinessRun) sortedScopes() []string {
	ids := make([]string, 0, len(r.scopes))
	for id := range r.scopes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (r *BusinessRun) bindCoverageScopes() error {
	for _, id := range r.sortedScopes() {
		scope := r.scopes[id]
		e := scope.envelope
		// Attempt observations are diagnostics, never a replacement for a terminal.
		if !e.Receipt.CoverageComplete || !e.Receipt.TerminalFact || e.CompletedAt == nil {
			continue
		}
		for _, entry := range r.entries {
			if scopeMatches(e, entry.subject) {
				if err := r.bindGoReceipt(entry.subject, &e.Receipt, e.FullConfigDigest, e.BusinessConfigDigest, time.UnixMilli(*e.CompletedAt)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (r *BusinessRun) finalizeCoverageScopes(now, pythonCompleted time.Time, grace time.Duration) error {
	for _, id := range r.sortedScopes() {
		scope := r.scopes[id]
		e := scope.envelope
		if scope.audit != nil {
			continue
		}
		completed := pythonCompleted
		if e.CompletedAt != nil && time.UnixMilli(*e.CompletedAt).After(completed) {
			completed = time.UnixMilli(*e.CompletedAt)
		}
		if now.Before(completed.Add(grace)) {
			continue
		}
		a := &BusinessAudit{Schema: "business-comparison-audit-v1", Epoch: r.epoch, SubjectKind: "COVERAGE", ScopeIdentity: e.Identity, ReceiptDigest: e.Digest, Eligibility: "UNPROVEN", Reason: "GO_SCOPE_OBSERVATION_ONLY", Differences: []string{}, FirstSeen: scope.auditFirst, Completed: completed.UnixMilli(), GraceDeadline: completed.Add(grace).UnixMilli()}
		a.Go = &BusinessEvidenceRef{Digest: e.Digest, Delivery: scope.locator}
		a.CaptureCounts = r.captureCountsCopy()
		a.ID, _ = contract.DeriveCanonicalDigestV2("business-audit-id-v1", []string{r.epoch, e.Identity, "COVERAGE"})
		if e.Receipt.CoverageComplete && e.CompletedAt != nil {
			count := uint64(0)
			for _, entry := range r.entries {
				if entry.goResult != nil && entry.goResult.Context != nil && *entry.goResult.Context == e.Receipt.Context {
					count++
				}
			}
			if count == *e.Receipt.Records.PrimaryAbnormal.Value {
				a.Eligibility = "ELIGIBLE_FULL"
				a.Reason = "GO_SCOPE_CLOSED"
			} else {
				a.Reason = "GO_EVIDENCE_COVERAGE_GAP"
				r.invalid = true
			}
		}
		wire, err := EncodeBusinessAudit(a, r.limits.MessageBytes)
		if err != nil {
			return err
		}
		cost := len(wire) + 128
		if r.bytes > r.limits.Bytes-cost {
			return ErrBusinessBackpressure
		}
		r.bytes += cost
		scope.audit = a
	}
	return nil
}
func (r *BusinessRun) offsetAcknowledged(key string) bool {
	if strings.HasPrefix(key, coverageOffsetPrefix) {
		scope := r.scopes[strings.TrimPrefix(key, coverageOffsetPrefix)]
		return scope != nil && scope.acked
	}
	entry := r.entries[key]
	return entry != nil && entry.acked
}
func (r *BusinessRun) publishCoverageScopes(ctx context.Context, sink BusinessAuditSink, expired func() bool) error {
	for _, id := range r.sortedScopes() {
		scope := r.scopes[id]
		if scope.audit == nil || scope.acked {
			continue
		}
		ready := true
		for _, entry := range r.entries {
			if scopeMatches(scope.envelope, entry.subject) && !entry.acked {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		if expired != nil && expired() {
			_ = r.Gap("COMPARATOR_CAPACITY_GAP")
			return r.PublishPending(ctx, sink)
		}
		wire, err := EncodeBusinessAudit(scope.audit, r.limits.MessageBytes)
		if err != nil {
			return err
		}
		var copy BusinessAudit
		if err = json.Unmarshal(wire, &copy); err != nil {
			return err
		}
		if err = sink.WriteBusinessAudit(ctx, &copy); err != nil {
			return err
		}
		if expired != nil && expired() {
			_ = r.Gap("COMPARATOR_CAPACITY_GAP")
			return r.PublishPending(ctx, sink)
		}
		scope.acked = true
		for _, p := range r.partitions {
			r.advance(p)
		}
	}
	return nil
}

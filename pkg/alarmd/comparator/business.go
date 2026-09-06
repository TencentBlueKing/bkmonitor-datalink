package comparator

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// BusinessRun is one finite validation Epoch. The transport owns fetching and
// committing; this core never touches business offsets or execution state.
// Calls are serialized by its single consumer loop.
type BusinessRun struct {
	pythonStrategies map[string]bool
	target           *contract.ShadowTargetScopeV1
	epoch            string
	limits           BusinessLimits
	entries          map[string]*businessEntry
	partitions       map[BusinessPartition]*businessPartition
	bytes            int
	invalid          bool
	gap              *BusinessAudit
}
type BusinessLimits struct {
	Entries, Bytes, MessageBytes, OffsetEntries int
	MaxAge                                      time.Duration
}
type BusinessPartition struct {
	Chain, Topic string
	Partition    int32
}
type BusinessRange struct {
	BusinessPartition
	Start, End int64
}
type BusinessOffset struct {
	BusinessPartition
	Offset    int64
	RawSHA256 string
}
type businessPartition struct {
	start, end, read, committed int64
	pending                     map[int64]string
}
type businessEvidence struct {
	Context                  *contract.ShadowContextV1
	Digest, NativeID, Config string
	Primary                  contract.BusinessPrimaryV1
	Locator                  BusinessOffset
}
type businessEntry struct {
	subject          contract.ShadowSubjectV1
	python, goResult *businessEvidence
	first            time.Time
	conflict         bool
	goClosed         bool
	goCompleted      time.Time
	goConfig         string
	receiptDigest    string
	receiptContext   *contract.ShadowContextV1
	expectedAbnormal uint64
	audit            *BusinessAudit
	acked            bool
}
type BusinessAudit struct {
	Schema        string                   `json:"schema"`
	Epoch         string                   `json:"validation_epoch_id"`
	ID            string                   `json:"audit_id"`
	SubjectKind   string                   `json:"subject_kind"`
	Subject       contract.ShadowSubjectV1 `json:"comparison_subject_ref"`
	Eligibility   string                   `json:"eligibility"`
	Verdict       string                   `json:"verdict"`
	Reason        string                   `json:"reason_code"`
	Python        *BusinessEvidenceRef     `json:"python_evidence_ref,omitempty"`
	Go            *BusinessEvidenceRef     `json:"go_evidence_ref,omitempty"`
	Differences   []string                 `json:"semantic_differences"`
	FirstSeen     int64                    `json:"first_seen"`
	Completed     int64                    `json:"completed"`
	GraceDeadline int64                    `json:"grace_deadline"`
	ReceiptDigest string                   `json:"go_receipt_digest"`
}
type BusinessEvidenceRef struct {
	Digest   string         `json:"evidence_digest"`
	EventID  string         `json:"event_id"`
	Delivery BusinessOffset `json:"delivery"`
}

func EncodeBusinessAudit(a *BusinessAudit, limit int) ([]byte, error) {
	if a == nil || a.Schema != "business-comparison-audit-v1" || a.Epoch == "" || a.Differences == nil {
		return nil, errors.New("business Audit header")
	}
	var want string
	switch a.SubjectKind {
	case "POINT":
		want, _ = contract.DeriveCanonicalDigestV2("business-audit-id-v1", []string{a.Epoch, businessKey(a.Subject), "POINT"})
		if a.Eligibility != "ELIGIBLE_BUSINESS_RESULT" && a.Eligibility != "UNPROVEN" {
			return nil, errors.New("business Audit eligibility")
		}
		switch a.Verdict {
		case "MATCHED_SAME", "MATCHED_DIFF", "PYTHON_ONLY", "GO_ONLY":
		case "":
			if a.Eligibility != "UNPROVEN" {
				return nil, errors.New("business Audit verdict")
			}
		default:
			return nil, errors.New("business Audit verdict")
		}
	case "EPOCH_GAP":
		want, _ = contract.DeriveCanonicalDigestV2("business-audit-id-v1", []string{a.Epoch, "EPOCH_GAP"})
		if a.Eligibility != "UNPROVEN" || a.Verdict != "" {
			return nil, errors.New("business gap verdict")
		}
	default:
		return nil, errors.New("business Audit kind")
	}
	if want != a.ID || a.Reason == "" {
		return nil, errors.New("business Audit identity")
	}
	wire, err := contract.CanonicalJSONV2(a)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || len(wire) > limit {
		return nil, errors.New("business Audit size")
	}
	return wire, nil
}

var ErrBusinessBackpressure = errors.New("business comparator capacity: pause input")

func NewBusinessRun(epoch string, ranges []BusinessRange, limits BusinessLimits) (*BusinessRun, error) {
	if epoch == "" || len(ranges) == 0 || len(ranges) > 256 || limits.Entries <= 0 || limits.Bytes <= 0 || limits.MessageBytes <= 0 || limits.OffsetEntries <= 0 || limits.MaxAge <= 0 {
		return nil, errors.New("business comparator limits or Epoch")
	}
	r := &BusinessRun{epoch: epoch, limits: limits, entries: map[string]*businessEntry{}, partitions: map[BusinessPartition]*businessPartition{}}
	for _, p := range ranges {
		if (p.Chain != contract.ShadowGo && p.Chain != contract.ShadowPython) || p.Topic == "" || p.Partition < 0 || p.Start < 0 || p.End < p.Start {
			return nil, errors.New("business range")
		}
		if _, ok := r.partitions[p.BusinessPartition]; ok {
			return nil, errors.New("duplicate business partition")
		}
		r.partitions[p.BusinessPartition] = &businessPartition{start: p.Start, end: p.End, read: p.Start, committed: p.Start, pending: map[int64]string{}}
	}
	chains := map[string]bool{}
	for p := range r.partitions {
		chains[p.Chain] = true
	}
	if !chains[contract.ShadowGo] || !chains[contract.ShadowPython] {
		return nil, errors.New("both frozen business streams required")
	}
	return r, nil
}
func businessKey(s contract.ShadowSubjectV1) string {
	key, _ := contract.DeriveCanonicalDigestV2("business-subject-v1", s)
	return key
}

// Observe validates before retaining compact summaries, never full config or
// Dataset. Input quality UNKNOWN does not disqualify actual Python output.
func (r *BusinessRun) Observe(at time.Time, offset BusinessOffset, wire []byte) error {
	if r.gap != nil {
		return errors.New("business Epoch closed by gap")
	}
	p, ok := r.partitions[offset.BusinessPartition]
	if !ok || offset.Offset != p.read || offset.Offset >= p.end {
		return errors.New("business offset outside frozen range")
	}
	if r.offsetCount() >= r.limits.OffsetEntries {
		return ErrBusinessBackpressure
	}
	var e *contract.BusinessAbnormalV1
	var frozen *contract.ShadowContextV1
	var err error
	if offset.Chain == contract.ShadowGo {
		var envelope *contract.GoBusinessAbnormalV1
		envelope, err = contract.DecodeGoBusinessAbnormalV1(wire, r.limits.MessageBytes)
		if err == nil {
			if envelope.EpochID != r.epoch {
				return errors.New("Go business Epoch mismatch")
			}
			e = &envelope.Reference
			frozen = &envelope.Context
		}
	} else {
		e, err = contract.DecodeBusinessAbnormalV1(wire, r.limits.MessageBytes)
	}
	if err != nil {
		return err
	} // caller must persist a gap Audit; never skip malformed input.
	if r.target != nil && (e.Subject.TenantID != r.target.TenantID || e.Subject.BusinessID != r.target.BusinessID || !r.pythonStrategies[e.Subject.StrategyID]) {
		return errors.New("business result outside frozen target")
	}
	key := businessKey(e.Subject)
	old := r.entries[key]
	if old != nil && old.audit != nil {
		r.invalid = true
		return errors.New("business late after terminal")
	}
	if old == nil && len(r.entries) >= r.limits.Entries {
		return ErrBusinessBackpressure
	}
	fact := &businessEvidence{Context: frozen, Digest: e.SemanticDigest, NativeID: e.Native.EventID, Config: e.ConfigDigest, Primary: e.Primary, Locator: offset}
	encoded, _ := json.Marshal(fact)
	// Retained summaries and offset references are charged; no unbounded replay list.
	cost := len(encoded) + len(key) + 128
	if r.bytes > r.limits.Bytes-cost {
		return ErrBusinessBackpressure
	}
	if old == nil {
		old = &businessEntry{subject: e.Subject, first: at}
		r.entries[key] = old
	}
	var target **businessEvidence
	if offset.Chain == contract.ShadowPython {
		target = &old.python
	} else {
		target = &old.goResult
	}
	if *target != nil {
		if (*target).Digest != fact.Digest {
			old.conflict = true
			r.invalid = true
		}
	} else {
		*target = fact
	}
	r.bytes += cost
	p.pending[offset.Offset] = key
	p.read++
	return nil
}
func (r *BusinessRun) offsetCount() int {
	n := 0
	for _, p := range r.partitions {
		n += len(p.pending)
	}
	return n
}

// SkipNative acknowledges classification only. A later bare native event can
// never jump over an earlier evidence record waiting for Audit broker ACK.
func (r *BusinessRun) SkipNative(offset BusinessOffset) error {
	p, ok := r.partitions[offset.BusinessPartition]
	if !ok || offset.Chain != contract.ShadowGo || offset.Offset != p.read || offset.Offset >= p.end {
		return errors.New("native offset")
	}
	if r.offsetCount() >= r.limits.OffsetEntries {
		return ErrBusinessBackpressure
	}
	p.pending[offset.Offset] = ""
	p.read++
	r.advance(p)
	return nil
}

// BindGoReceipt uses the existing validated receipt and actual full config to
// establish source-window→Slot mapping. It cannot infer terminal from time.
func (r *BusinessRun) BindGoReceipt(subject contract.ShadowSubjectV1, receipt *contract.ChainCoverageReceiptV1, full contract.ComparisonConfigV2, completed time.Time) error {
	if err := contract.ValidateChainCoverageReceiptV1(receipt); err != nil {
		return err
	}
	_, fullDigest, err := contract.CanonicalComparisonConfigV2(full)
	if err != nil {
		return err
	}
	_, configDigest, err := contract.CanonicalBusinessConfigV1(full)
	if err != nil {
		return err
	}
	if receipt.Chain != contract.ShadowGo || receipt.EpochID != r.epoch || receipt.TenantID != subject.TenantID || receipt.BusinessID != subject.BusinessID || receipt.StrategyID != subject.StrategyID || receipt.Context.ComparisonConfigDigest != fullDigest || !receipt.CoverageComplete || !receipt.TerminalFact || completed.IsZero() {
		return errors.New("business receipt closure")
	}
	if subject.SourceTime < receipt.Input.SourceWindow.FromTime || subject.SourceTime >= receipt.Input.SourceWindow.UntilTime {
		return errors.New("business source not mapped to Slot")
	}
	e := r.entries[businessKey(subject)]
	if e == nil {
		return errors.New("business subject absent")
	}
	digest, err := contract.DeriveCanonicalDigestV2("business-go-receipt-v1", receipt)
	if err != nil {
		return err
	}
	if e.goClosed && (e.receiptDigest != digest || e.goConfig != configDigest) {
		r.invalid = true
		return errors.New("business receipt conflict")
	}
	if e.goResult != nil && (e.goResult.Context == nil || *e.goResult.Context != receipt.Context) {
		return errors.New("Go result/Receipt frozen context mismatch")
	}
	contextCopy := receipt.Context
	e.receiptContext = &contextCopy
	e.expectedAbnormal = *receipt.Records.PrimaryAbnormal.Value
	e.goClosed = true
	e.goConfig = configDigest
	e.goCompleted = completed
	e.receiptDigest = digest
	return nil
}

// Finalize requires actual range completion on BOTH streams, an independently
// proven Python completion time and per-subject Go Receipt. Grace expiry alone
// never creates missing results. Failed/partial capture must call Gap instead.
func (r *BusinessRun) Finalize(now, pythonCompleted time.Time, grace time.Duration) ([]BusinessAudit, error) {
	if pythonCompleted.IsZero() || grace < 0 {
		return nil, errors.New("business completion/grace missing")
	}
	for _, p := range r.partitions {
		if p.read != p.end {
			return nil, nil
		}
	}
	keys := make([]string, 0, len(r.entries))
	for k := range r.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	counts := make(map[contract.ShadowContextV1]uint64, len(r.entries))
	for _, e := range r.entries {
		if e.goResult != nil && e.goResult.Context != nil {
			counts[*e.goResult.Context]++
		}
	}
	var out []BusinessAudit
	for _, key := range keys {
		e := r.entries[key]
		if e.audit != nil {
			continue
		}
		if !e.goClosed {
			continue
		}
		completed := pythonCompleted
		if e.goCompleted.After(completed) {
			completed = e.goCompleted
		}
		deadline := completed.Add(grace)
		if now.Before(deadline) {
			continue
		}
		a := BusinessAudit{Schema: "business-comparison-audit-v1", Epoch: r.epoch, SubjectKind: "POINT", Subject: e.subject, Eligibility: "ELIGIBLE_BUSINESS_RESULT", Differences: []string{}, FirstSeen: e.first.UnixMilli(), Completed: completed.UnixMilli(), GraceDeadline: deadline.UnixMilli(), ReceiptDigest: e.receiptDigest}
		a.ID, _ = contract.DeriveCanonicalDigestV2("business-audit-id-v1", []string{r.epoch, key, "POINT"})
		ref := func(f *businessEvidence) *BusinessEvidenceRef {
			if f == nil {
				return nil
			}
			return &BusinessEvidenceRef{f.Digest, f.NativeID, f.Locator}
		}
		a.Python = ref(e.python)
		a.Go = ref(e.goResult)
		switch {
		case e.receiptContext != nil && counts[*e.receiptContext] != e.expectedAbnormal:
			a.Eligibility = "UNPROVEN"
			a.Reason = "GO_EVIDENCE_COVERAGE_GAP"
		case e.conflict:
			a.Eligibility = "UNPROVEN"
			a.Reason = "SEMANTIC_CONFLICT"
		case e.python != nil && e.python.Config != e.goConfig:
			a.Eligibility = "UNPROVEN"
			a.Reason = "CONFIG_NOT_EQUIVALENT"
		case e.goResult != nil && e.goResult.Config != e.goConfig:
			a.Eligibility = "UNPROVEN"
			a.Reason = "GO_CONFIG_CONFLICT"
		case e.python == nil:
			a.Verdict = "GO_ONLY"
			a.Reason = "INDEPENDENT_GO_AUDIT_REQUIRED"
		case e.goResult == nil:
			a.Verdict = "PYTHON_ONLY"
			a.Reason = "GO_RESULT_MISSING"
		case e.python.Digest == e.goResult.Digest:
			a.Verdict = "MATCHED_SAME"
			a.Reason = "KEY_FIELDS_EQUAL"
		default:
			a.Verdict = "MATCHED_DIFF"
			a.Reason = "KEY_FIELDS_DIFFER"
			a.Differences = []string{"primary"}
		}
		wire, err := EncodeBusinessAudit(&a, r.limits.MessageBytes)
		if err != nil {
			return nil, err
		}
		// Reserve retained Audit bytes before installing the terminal payload.
		cost := len(wire) + 128
		if r.bytes > r.limits.Bytes-cost {
			return nil, ErrBusinessBackpressure
		}
		r.bytes += cost
		e.audit = &a
		var copy BusinessAudit
		_ = json.Unmarshal(wire, &copy)
		out = append(out, copy)
	}
	return out, nil
}

// ACKAudit is called ONLY after the Audit sink confirms broker ACK. Digest
// binding rejects a token for a different terminal payload.
func (r *BusinessRun) ACKAudit(id, digest string) error {
	for _, e := range r.entries {
		if e.audit == nil || e.audit.ID != id {
			continue
		}
		want, err := contract.DeriveCanonicalDigestV2("business-audit-payload-v1", e.audit)
		if err != nil || want != digest {
			return errors.New("business audit ACK payload mismatch")
		}
		e.acked = true
		for _, p := range r.partitions {
			r.advance(p)
		}
		return nil
	}
	return errors.New("business audit ACK unknown")
}
func (r *BusinessRun) advance(p *businessPartition) {
	for p.committed < p.read {
		key, ok := p.pending[p.committed]
		if !ok {
			return
		}
		if key != "" && !r.entries[key].acked {
			return
		}
		delete(p.pending, p.committed)
		p.committed++
	}
}
func (r *BusinessRun) Committable(p BusinessPartition) (int64, error) {
	state, ok := r.partitions[p]
	if !ok {
		return 0, errors.New("business partition")
	}
	return state.committed, nil
}
func (r *BusinessRun) Result() string {
	if r.invalid {
		return "UNPROVEN"
	}
	for _, p := range r.partitions {
		if p.committed != p.end {
			return "PENDING"
		}
	}
	result := "MATCHED_SAME"
	for _, e := range r.entries {
		if e.audit == nil || !e.acked {
			return "PENDING"
		}
		if e.audit.Verdict == "PYTHON_ONLY" || e.audit.Verdict == "MATCHED_DIFF" {
			return "FAILED"
		}
		if e.audit.Eligibility == "UNPROVEN" {
			return "UNPROVEN"
		}
		if e.audit.Verdict == "GO_ONLY" {
			result = "GO_ONLY_REVIEW_REQUIRED"
		}
	}
	if len(r.entries) == 0 {
		return "NO_ABNORMAL_NOT_EPOCH_PASS"
	}
	return result // this is the ABNORMAL result cohort, never a whole G5 verdict.
}

type BusinessAuditSink interface {
	WriteBusinessAudit(context.Context, *BusinessAudit) error
}

// PublishPending keeps one bounded Audit write in flight. It never marks an
// input offset before the real sink call confirms broker ACK.
func (r *BusinessRun) PublishPending(ctx context.Context, sink BusinessAuditSink) error {
	if sink == nil {
		return errors.New("business Audit sink required")
	}
	if r.gap != nil {
		if err := sink.WriteBusinessAudit(ctx, r.gap); err != nil {
			return err
		}
		// A gap closes this Epoch. Only already-read offsets can advance; unread
		// source ranges are never presented as consumed or reset here.
		for _, p := range r.partitions {
			p.committed = p.read
			clear(p.pending)
		}
		clear(r.entries)
		r.bytes = 0
		return nil
	}
	keys := make([]string, 0, len(r.entries))
	for k := range r.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		e := r.entries[key]
		if e.audit == nil || e.acked {
			continue
		}
		wire, err := contract.CanonicalJSONV2(e.audit)
		if err != nil {
			return err
		}
		if len(wire) > r.limits.MessageBytes {
			return errors.New("business Audit size exceeds limit")
		}
		// Detach the sink argument so a sink cannot mutate the terminal payload
		// whose digest authorizes offset advancement.
		var detached BusinessAudit
		if err = json.Unmarshal(wire, &detached); err != nil {
			return err
		}
		if err = sink.WriteBusinessAudit(ctx, &detached); err != nil {
			return err
		}
		digest, err := contract.DeriveCanonicalDigestV2("business-audit-payload-v1", e.audit)
		if err != nil {
			return err
		}
		if err = r.ACKAudit(e.audit.ID, digest); err != nil {
			return err
		}
	}
	return nil
}

// Gap handles an actual read/codec/retention/capacity failure. Only a fixed
// reason may enter Audit. It invalidates the Epoch even after release.
func (r *BusinessRun) Gap(reason string) error {
	switch reason {
	case "REFERENCE_GAP", "EVIDENCE_GAP", "OFFSET_GAP", "COMPARATOR_CAPACITY_GAP", "COMPARATOR_RESTART_GAP":
	default:
		return errors.New("business gap reason")
	}
	r.invalid = true
	if r.gap != nil {
		return nil
	}
	id, err := contract.DeriveCanonicalDigestV2("business-audit-id-v1", []string{r.epoch, "EPOCH_GAP"})
	if err != nil {
		return err
	}
	r.gap = &BusinessAudit{Schema: "business-comparison-audit-v1", Epoch: r.epoch, ID: id, SubjectKind: "EPOCH_GAP", Eligibility: "UNPROVEN", Reason: reason, Differences: []string{}}
	return nil
}
func (r *BusinessRun) Expire(now time.Time) error {
	for _, e := range r.entries {
		if !e.acked && now.Sub(e.first) > r.limits.MaxAge {
			return r.Gap("COMPARATOR_CAPACITY_GAP")
		}
	}
	return nil
}

// StrategyNormal is a coarse comparison fact, never a series NORMAL ledger.
type StrategyNormal struct {
	StrategyID, ExecutionRef string
	CompletedAt              int64
	AnomalyRecords           uint64
	Completed                bool
}

func CompareStrategyNormal(python StrategyNormal, goCompleted bool, goAbnormal uint64) string {
	if !python.Completed || python.StrategyID == "" || python.ExecutionRef == "" || python.CompletedAt <= 0 || !goCompleted {
		return "UNPROVEN"
	}
	if python.AnomalyRecords != 0 {
		return "ABNORMAL_REQUIRES_EXACT_KAFKA_COMPARISON"
	}
	if goAbnormal != 0 {
		return "GO_ONLY_REVIEW_REQUIRED"
	}
	return "STRATEGY_NORMAL_COARSE_AGREEMENT"
}

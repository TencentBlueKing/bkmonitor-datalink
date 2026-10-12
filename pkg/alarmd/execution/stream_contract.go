package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

type PlannedPhysicalQueryRef struct {
	Digest        PhysicalQueryDigest
	QueryRevision QueryRevision
}

type InternalExecutionHeader struct {
	ExecutionID             string
	Contract                FrozenExecutionContractRef
	DuePlans                []DuePlan
	Requirements            []DataRequirement
	EffectiveTimeFacts      []BoundEffectiveTimeFact
	RequiredPhysicalQueries []PlannedPhysicalQueryRef
	DeadlineUnixMilli       int64
}

func (header InternalExecutionHeader) Validate(expected FrozenExecutionContractRef) error {
	if err := header.Contract.Validate(); err != nil {
		return err
	}
	if header.Contract != expected || header.ExecutionID == "" || header.DeadlineUnixMilli <= 0 ||
		len(header.DuePlans) == 0 || len(header.Requirements) == 0 || len(header.RequiredPhysicalQueries) == 0 {
		return errors.New("alarmd execution: incomplete internal execution header")
	}
	plans := make(map[PlanIdentity]DuePlan, len(header.DuePlans))
	for _, due := range header.DuePlans {
		if err := due.Identity.Validate(); err != nil {
			return err
		}
		if due.CompiledPlan == nil || due.StateGeneration == "" || due.StateApplyEpoch == 0 ||
			due.ScheduleRevision == "" || due.CompletionDeadlineUnixMilli <= 0 {
			return errors.New("alarmd execution: incomplete due Plan in execution header")
		}
		if _, duplicate := plans[due.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate due Plan in execution header")
		}
		plans[due.Identity] = due
	}
	for _, requirement := range header.Requirements {
		if err := requirement.Validate(plans); err != nil {
			return err
		}
	}
	digest, err := DeriveDuePlanSetDigest(header.DuePlans, header.Requirements)
	if err != nil || digest != header.Contract.DuePlanSetDigest {
		return errors.New("alarmd execution: header due Plan set differs from frozen contract")
	}
	seen := make(map[PhysicalQueryDigest]struct{}, len(header.RequiredPhysicalQueries))
	for _, query := range header.RequiredPhysicalQueries {
		if query.Digest == "" || query.QueryRevision == "" {
			return errors.New("alarmd execution: incomplete required physical query reference")
		}
		if _, duplicate := seen[query.Digest]; duplicate {
			return errors.New("alarmd execution: duplicate required physical query reference")
		}
		seen[query.Digest] = struct{}{}
	}
	return nil
}

type SeriesExecutionBatch struct {
	PhysicalQuery PhysicalQueryDigest
	QueryRevision QueryRevision
	CompletionRef ProviderResultRef
	Dataset       *Dataset
	Inputs        []NamedInputBinding
	Delivery      SeriesDelivery
}

func (batch SeriesExecutionBatch) Validate(header InternalExecutionHeader) error {
	if batch.PhysicalQuery == "" || batch.QueryRevision == "" || batch.CompletionRef == "" ||
		batch.Dataset == nil || batch.Dataset.Len() == 0 || len(batch.Inputs) == 0 ||
		batch.Delivery.PhysicalQuery != batch.PhysicalQuery || batch.Delivery.QueryRevision != batch.QueryRevision ||
		batch.Delivery.Series != 1 || batch.Delivery.Records != uint64(batch.Dataset.Len()) || batch.Delivery.Digest == "" {
		return errors.New("alarmd execution: incomplete series execution batch")
	}
	found := false
	for _, query := range header.RequiredPhysicalQueries {
		if query.Digest == batch.PhysicalQuery && query.QueryRevision == batch.QueryRevision {
			found = true
			break
		}
	}
	if !found {
		return errors.New("alarmd execution: series batch differs from frozen physical queries")
	}
	for _, binding := range batch.Inputs {
		if binding.Dataset != batch.Dataset || binding.View == nil || !binding.View.Uses(batch.Dataset) ||
			binding.Provenance.PhysicalQuery != batch.PhysicalQuery || binding.ProviderResult != batch.CompletionRef {
			return errors.New("alarmd execution: series batch binding does not reference its immutable dataset")
		}
	}
	return nil
}

func DeriveStreamingPrimaryInputFact(header InternalExecutionHeader, bindings []NamedInputBinding) (PrimaryInputFact, error) {
	return DerivePrimaryInputFact(InternalExecution{
		Contract: header.Contract, DuePlans: header.DuePlans, Requirements: header.Requirements, Inputs: bindings,
	})
}

// DeriveStreamingCompletionAttribution reports the kind with the cause, the
// reason belonging to the cause and the scope the cause was found in. The
// reason is the level the answer usually lives at: the cause says a Level
// could not be decided, the reason says whether that is the data not reaching
// this window or something that clears on its own.
func DeriveStreamingCompletionAttribution(
	header InternalExecutionHeader,
	bindings []NamedInputBinding,
	result EvaluationResult,
) (CompletionKind, CompletionAttribution, error) {
	return DeriveCompletionAttribution(InternalExecution{
		Contract: header.Contract, DuePlans: header.DuePlans, Requirements: header.Requirements, Inputs: bindings,
	}, result)
}

type ProviderSeriesBatch struct {
	PhysicalQuery PhysicalQueryDigest
	CompletionRef ProviderResultRef
	Dataset       *Dataset
	Delivery      SeriesDelivery
}

type ProviderCompletion struct {
	Ref             ProviderResultRef
	PhysicalQuery   PhysicalQueryDigest
	Completeness    Completeness
	DataState       DataState
	Delivery        SeriesDelivery
	RouteFacts      ProviderRouteFacts
	PartialEvidence *PartialEvidence
	Stats           ProviderStats
	// Withheld is how many series the provider returned that every Plan the
	// query feeds refused, and WithheldOutsideTarget how many of them every
	// Plan refused as outside its monitoring target, decided on facts that
	// were all there (admission.DefinitelyOutside). Both are zero where no
	// target filters. A query whose every series was withheld completes
	// EMPTY, as it would had none been returned; these say it was not.
	Withheld              uint64
	WithheldOutsideTarget uint64
}

type ProviderSeriesSink interface {
	ConsumeProviderSeries(context.Context, ProviderSeriesBatch) error
}

type QueryExecutionConsumer interface {
	Begin(context.Context, InternalExecutionHeader) error
	ConsumeSeries(context.Context, SeriesExecutionBatch) error
	// ResolvedTargets is what this execution resolved each target-plan Plan's
	// target to, by Plan, read by the source after Begin so the admission
	// filter and the consumer's own absence judgement see one resolution. It
	// is part of the interface rather than an optional one: a consumer that
	// silently lacked it would have every target-plan Plan admit nothing,
	// and the only trace would be a rejection counter.
	ResolvedTargets() TargetMemberships
	// ResolvedScopeGroups is what this execution read the dynamic groups the
	// due Plans' target scopes name to, by tenant and group, read by the
	// source after Begin as ResolvedTargets is. A group it does not list is
	// one nobody read, and its condition admits only what it knows: nothing.
	ResolvedScopeGroups() ScopeGroupMemberships
}

// ScopeGroupRef names one dynamic group of one tenant.
type ScopeGroupRef struct {
	TenantID string
	GroupID  string
}

// ScopeGroupMembership is one dynamic group as an execution read it: the host
// ids it holds, and whether that is the whole group read from current facts.
type ScopeGroupMembership struct {
	HostIDs []string
	Known   bool
}

// ScopeGroupMemberships is one execution's reading of the dynamic groups its
// Plans' target scopes name.
type ScopeGroupMemberships map[ScopeGroupRef]ScopeGroupMembership

// TargetMembership answers whether a record key is among the members a
// target plan resolved to in one execution.
type TargetMembership interface {
	Contains(key string) bool
}

// TargetMemberships is one execution's target resolutions by Plan. An
// absent Plan, and a nil entry, both mean the target was not resolved.
type TargetMemberships map[PlanIdentity]TargetMembership

type SeriesDelivery struct {
	PhysicalQuery PhysicalQueryDigest
	QueryRevision QueryRevision
	Series        uint64
	Records       uint64
	Bytes         uint64
	Digest        string
}

// AccumulateSeriesDelivery folds ordered batch delivery proofs for one
// PhysicalQuery. A single batch keeps its provider digest; every following
// batch extends the proof instead of replacing it with the last digest.
func AccumulateSeriesDelivery(current, next SeriesDelivery) (SeriesDelivery, error) {
	if next.PhysicalQuery == "" || next.QueryRevision == "" || next.Series == 0 || next.Digest == "" {
		return SeriesDelivery{}, errors.New("alarmd execution: incomplete series delivery")
	}
	if current.PhysicalQuery == "" {
		return next, nil
	}
	if current.PhysicalQuery != next.PhysicalQuery || current.QueryRevision != next.QueryRevision || current.Digest == "" {
		return SeriesDelivery{}, errors.New("alarmd execution: cannot accumulate unrelated series deliveries")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("alarmd-series-delivery-v1\x00"))
	_, _ = hash.Write([]byte(current.Digest))
	_, _ = hash.Write([]byte("\x00"))
	_, _ = hash.Write([]byte(next.Digest))
	current.Series += next.Series
	current.Records += next.Records
	current.Bytes += next.Bytes
	current.Digest = hex.EncodeToString(hash.Sum(nil))
	return current, nil
}

type PhysicalQueryCompletion struct {
	Ref             ProviderResultRef
	PhysicalQuery   PhysicalQueryDigest
	QueryRevision   QueryRevision
	Completeness    Completeness
	DataState       DataState
	Delivery        SeriesDelivery
	RouteFacts      ProviderRouteFacts
	PartialEvidence *PartialEvidence
	Stats           ProviderStats
	// Withheld is how many series the provider returned that every Plan the
	// query feeds refused, and WithheldOutsideTarget how many of them every
	// Plan refused as outside its monitoring target, decided on facts that
	// were all there (admission.DefinitelyOutside). Both are zero where no
	// target filters. A query whose every series was withheld completes
	// EMPTY, as it would had none been returned; these say it was not.
	Withheld              uint64
	WithheldOutsideTarget uint64
	// Range is how long a range the query asked the provider for and how
	// long a range it accepts, and whether it serves a primary requirement:
	// the request's facts, from the spec that was sent. Nil from a source
	// that does not name it.
	Range *PhysicalQueryRange `json:",omitempty"`
	// Clock is what the query had of its time from the moment it was sent
	// and what it used. Nil from a source that does not measure it.
	Clock *PhysicalQueryClock `json:",omitempty"`
}

// PhysicalQueryRange is a physical query's asked and accepted range lengths
// in seconds. They differ for an event count, which asks from its lead
// earlier so a quiet group's zeros come back, and accepts only its window.
type PhysicalQueryRange struct {
	Primary         bool
	AskedSeconds    int64
	AcceptedSeconds int64
}

type QueryExecutionCompletion struct {
	PhysicalQueries      []PhysicalQueryCompletion
	CompletionBindings   []NamedInputBinding
	AllRequiredCompleted bool
}

func (completion QueryExecutionCompletion) Validate(header InternalExecutionHeader, delivered []SeriesDelivery) error {
	if !completion.AllRequiredCompleted {
		return errors.New("alarmd execution: all required physical queries must be completed")
	}
	required := make(map[PhysicalQueryDigest]QueryRevision, len(header.RequiredPhysicalQueries))
	for _, query := range header.RequiredPhysicalQueries {
		required[query.Digest] = query.QueryRevision
	}
	deliveryByQuery := make(map[PhysicalQueryDigest]SeriesDelivery, len(delivered))
	for _, item := range delivered {
		if item.PhysicalQuery == "" || item.QueryRevision == "" || item.Digest == "" {
			return errors.New("alarmd execution: incomplete delivered series facts")
		}
		if _, duplicate := deliveryByQuery[item.PhysicalQuery]; duplicate {
			return errors.New("alarmd execution: duplicate delivered physical query facts")
		}
		deliveryByQuery[item.PhysicalQuery] = item
	}
	seen := make(map[PhysicalQueryDigest]struct{}, len(completion.PhysicalQueries))
	for _, item := range completion.PhysicalQueries {
		revision, ok := required[item.PhysicalQuery]
		if !ok || revision != item.QueryRevision || item.Ref == "" {
			return errors.New("alarmd execution: completion differs from frozen physical query")
		}
		if _, duplicate := seen[item.PhysicalQuery]; duplicate {
			return errors.New("alarmd execution: duplicate physical query completion")
		}
		seen[item.PhysicalQuery] = struct{}{}
		actual, deliveredAny := deliveryByQuery[item.PhysicalQuery]
		if item.DataState == DataStateData {
			if !deliveredAny || item.Delivery != actual || item.Delivery.Series == 0 || item.Delivery.Records == 0 {
				return errors.New("alarmd execution: physical completion does not conserve delivered series")
			}
		} else if deliveredAny || item.Delivery.Series != 0 || item.Delivery.Records != 0 {
			return errors.New("alarmd execution: empty or unavailable completion must not claim delivered series")
		}
	}
	if len(seen) != len(required) {
		return errors.New("alarmd execution: completion does not cover all frozen physical queries")
	}
	return nil
}

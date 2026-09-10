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

// buildSeriesInternalExecution binds one immutable series batch to the static
// header for package-local validation. It derives Runtime State identities;
// Access never supplies Redis keys or apply versions.
func buildSeriesInternalExecution(header InternalExecutionHeader, batch SeriesExecutionBatch) (InternalExecution, error) {
	if err := batch.Validate(header); err != nil {
		return InternalExecution{}, err
	}
	plansByID := make(map[PlanIdentity]DuePlan, len(header.DuePlans))
	selectedPlans := make(map[PlanIdentity]struct{})
	for _, due := range header.DuePlans {
		plansByID[due.Identity] = due
	}
	for _, binding := range batch.Inputs {
		selectedPlans[binding.Consumer.Plan] = struct{}{}
	}
	input := InternalExecution{Contract: header.Contract, Inputs: append([]NamedInputBinding(nil), batch.Inputs...)}
	for _, due := range header.DuePlans {
		if _, selected := selectedPlans[due.Identity]; selected {
			input.DuePlans = append(input.DuePlans, due)
		}
	}
	for _, requirement := range header.Requirements {
		filtered := requirement
		filtered.Consumers = nil
		for _, consumer := range requirement.Consumers {
			if _, selected := selectedPlans[consumer.Consumer.Plan]; selected {
				filtered.Consumers = append(filtered.Consumers, consumer)
			}
		}
		if len(filtered.Consumers) > 0 {
			input.Requirements = append(input.Requirements, filtered)
		}
	}
	series := make(map[StateKeyIdentity]struct{})
	for _, binding := range batch.Inputs {
		due, found := plansByID[binding.Consumer.Plan]
		if !found || binding.Role != InputRolePrimary {
			continue
		}
		for index := 0; index < binding.View.Len(); index++ {
			record, ok := binding.View.Record(index)
			if !ok || record.DimensionIdentityDigest() == "" {
				return InternalExecution{}, errors.New("alarmd execution: series batch lacks stable series identity")
			}
			series[StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration,
				SeriesIdentityDigest: SeriesIdentityDigest(record.DimensionIdentityDigest())}] = struct{}{}
		}
		for _, fact := range binding.QualityFacts {
			if fact.ImpactScope == ImpactSeries {
				series[StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration,
					SeriesIdentityDigest: fact.SeriesIdentity}] = struct{}{}
			}
		}
		for _, terminal := range binding.Terminals {
			if terminal.ImpactScope == ImpactSeries {
				series[StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration,
					SeriesIdentityDigest: terminal.SeriesIdentity}] = struct{}{}
			}
		}
	}
	for identity := range series {
		due := plansByID[identity.Plan]
		version, err := BuildApplyVersion(header.Contract, due.StateApplyEpoch)
		if err != nil {
			return InternalExecution{}, err
		}
		input.StatePreflight = append(input.StatePreflight, StatePreflightItem{Identity: identity, ApplyVersion: version})
	}
	for _, fact := range header.EffectiveTimeFacts {
		if _, selected := series[StateKeyIdentity{Plan: fact.Consumer.Plan,
			StateGeneration: plansByID[fact.Consumer.Plan].StateGeneration, SeriesIdentityDigest: fact.SeriesIdentity}]; selected {
			input.EffectiveTimeFacts = append(input.EffectiveTimeFacts, fact)
		}
	}
	for _, due := range input.DuePlans {
		version, err := BuildApplyVersion(header.Contract, due.StateApplyEpoch)
		if err != nil {
			return InternalExecution{}, err
		}
		input.GapPreflight = append(input.GapPreflight, PlanGapLoadItem{
			Identity:     PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
			ApplyVersion: version, ScheduleRevision: due.ScheduleRevision,
		})
	}
	return input, nil
}

// DeriveSeriesStatePreflight exposes only the State identities required by the
// Coordinator. InternalExecution remains an execution-package validation
// detail and is not the phase-two streaming hand-off model.
func DeriveSeriesStatePreflight(header InternalExecutionHeader, batch SeriesExecutionBatch) ([]StatePreflightItem, error) {
	input, err := buildSeriesInternalExecution(header, batch)
	if err != nil {
		return nil, err
	}
	return input.StatePreflight, nil
}

func DeriveStreamingPrimaryInputFact(header InternalExecutionHeader, bindings []NamedInputBinding) (PrimaryInputFact, error) {
	return DerivePrimaryInputFact(InternalExecution{
		Contract: header.Contract, DuePlans: header.DuePlans, Requirements: header.Requirements, Inputs: bindings,
	})
}

func DeriveStreamingCompletionKind(
	header InternalExecutionHeader,
	bindings []NamedInputBinding,
	result EvaluationResult,
) (CompletionKind, error) {
	return DeriveCompletionKind(InternalExecution{
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
}

type ProviderSeriesSink interface {
	ConsumeProviderSeries(context.Context, ProviderSeriesBatch) error
}

type QueryExecutionConsumer interface {
	Begin(context.Context, InternalExecutionHeader) error
	ConsumeSeries(context.Context, SeriesExecutionBatch) error
}

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

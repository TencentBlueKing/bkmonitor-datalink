// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group changes when the content a Worker executes for it changes,
// and only then. The published QueryGroup carries more than that content: a
// Plan's StrategyRef holds the source's update_time (or, when the source has
// none, a digest of the whole decoded strategy), and a Plan that publishes
// the Python-compatible protocol carries the source document verbatim for the
// conversion to read. Both move on every save, so the snapshot revision moved
// on every save, and every save cut a Segment for a Query Group whose
// execution had not changed.
//
// The two objects below split the Plan at that line. QueryGroupObject holds
// what execution reads and is what an ObjectDigest names; OutputContextObject
// holds what event rendering reads and is what an OutputContextDigest names.
// Each object lists its fields explicitly rather than embedding the published
// types, so a field added to EvaluationPlanV2 does not enter either digest
// until someone places it; the test next to this file refuses to compile the
// classification until every field of the published types is in exactly one
// column.
const (
	queryGroupObjectContractVersion = "alarmd-query-group-object-v1"
	// queryGroupObjectContractVersionV2 is written on an object whose Plan
	// carries a target_plan. The version is the digest domain and the first
	// thing a reader checks, so a reader that predates the field refuses the
	// object by name instead of decoding it without its target and running
	// the Plan on everything. Objects without the field keep v1, and with it
	// every digest they had.
	queryGroupObjectContractVersionV2 = "alarmd-query-group-object-v2"
	queryGroupObjectContractVersionV3 = "alarmd-query-group-object-v3"
	outputContextContractVersion      = "alarmd-output-context-v1"

	// The two contracts' version strings are a prefix and a number, and the
	// number is how a reader tells an object of a later contract from bytes
	// that are not an object at all: later is a rollout, the rest is
	// corruption. The latest numbers are the ones the known-version checks
	// above accept; a version bump moves both.
	queryGroupObjectContractPrefix = "alarmd-query-group-object-v"
	queryGroupObjectContractLatest = 3
	outputContextContractPrefix    = "alarmd-output-context-v"
	outputContextContractLatest    = 1
)

// newerContractVersion reports whether version names a later version of the
// contract whose versions are prefix followed by a number, latest being the
// last one this build reads.
func newerContractVersion(version, prefix string, latest int) bool {
	number, found := strings.CutPrefix(version, prefix)
	if !found {
		return false
	}
	parsed, err := strconv.Atoi(number)
	return err == nil && parsed > latest
}

// queryGroupObjectVersion is the contract version an object is written
// under: v2 as soon as one of its Plans carries the target's second frozen
// form, v1 otherwise.
func queryGroupObjectVersion(plans []QueryGroupPlanObject) string {
	for _, plan := range plans {
		if len(plan.EffectiveTimeSnapshot) > 0 {
			return queryGroupObjectContractVersionV3
		}
	}
	for _, plan := range plans {
		if plan.TargetPlan != nil {
			return queryGroupObjectContractVersionV2
		}
	}
	return queryGroupObjectContractVersion
}

// knownQueryGroupObjectVersion reports whether this build reads objects of
// that contract version.
func knownQueryGroupObjectVersion(version string) bool {
	return version == queryGroupObjectContractVersion || version == queryGroupObjectContractVersionV2 || version == queryGroupObjectContractVersionV3
}

// queryGroupObjectDomain reads the contract version a stored object declares
// and returns it as the domain its digest was derived in. A version this
// build does not know is refused here, before the bytes are trusted for
// anything: the digest check that follows would otherwise be made in the
// wrong domain and read as corruption.
func queryGroupObjectDomain(payload []byte) (string, error) {
	var header struct {
		ContractVersion string `json:"object_contract_version"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return "", err
	}
	if !knownQueryGroupObjectVersion(header.ContractVersion) {
		if newerContractVersion(header.ContractVersion, queryGroupObjectContractPrefix, queryGroupObjectContractLatest) {
			return "", ErrCatalogObjectContractNewer
		}
		return "", errors.New("not a Query Group object of this contract")
	}
	return header.ContractVersion, nil
}

// outputContextDomain is queryGroupObjectDomain for output contexts: the one
// version this build reads is the domain, a later version is refused as
// newer, anything else is not an output context.
func outputContextDomain(payload []byte) (string, error) {
	var header struct {
		ContractVersion string `json:"output_context_contract_version"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return "", err
	}
	if header.ContractVersion != outputContextContractVersion {
		if newerContractVersion(header.ContractVersion, outputContextContractPrefix, outputContextContractLatest) {
			return "", ErrCatalogObjectContractNewer
		}
		return "", errors.New("not an output context of this contract")
	}
	return header.ContractVersion, nil
}

// QueryGroupObject is the execution content of one Query Group.
type QueryGroupObject struct {
	ContractVersion  string                       `json:"object_contract_version"`
	Identity         execution.QueryGroupIdentity `json:"query_group_identity"`
	ScheduleRevision execution.ScheduleRevision   `json:"query_group_schedule_revision"`
	QueryPlan        execution.QueryPlanFacts     `json:"query_plan"`
	Plans            []QueryGroupPlanObject       `json:"plans"`
	MembershipDigest string                       `json:"membership_digest"`
}

// QueryGroupPlanObject is the execution content of one Plan inside a Query
// Group. StateGeneration is derived from this same content at activation and
// is carried so a Worker can key state from the object alone; it adds no
// transition the other fields do not already cause.
//
// FrozenPlan.PlanRevision is absent on purpose. It is a digest over the whole
// EvaluationPlanV2, so it carries the update_time and the source document
// this object exists to exclude, and nothing in the module reads it: the two
// places that assign it are its only references. It is not a field waiting
// for a consumer; it is the old revision, and the ObjectDigest replaces it.
type QueryGroupPlanObject struct {
	EffectiveTimeSnapshot json.RawMessage           `json:"effective_time_snapshot,omitempty"`
	Identity              execution.PlanIdentity    `json:"plan_identity"`
	StateGeneration       execution.StateGeneration `json:"state_generation,omitempty"`
	// LevelContractRefs are the Leader's Level contract references, beside
	// the generation they were derived with. Omitted when nil: an object
	// written before the field keeps its bytes and its digest, and a reader
	// that predates the field decodes past it and derives its own, which is
	// what it did before.
	LevelContractRefs       []LevelContractRefObject                               `json:"level_contract_refs,omitempty"`
	NoDataLevelContractRefs []LevelContractRefObject                               `json:"no_data_level_contract_refs,omitempty"`
	ScheduleSpec            execution.ScheduleSpec                                 `json:"schedule_spec"`
	ScheduleRevision        execution.PlanScheduleRevision                         `json:"plan_schedule_revision"`
	RequirementTemplates    []execution.DataRequirementTemplate                    `json:"requirement_templates,omitempty"`
	QueryPlans              map[execution.LogicalQueryRef]execution.QueryPlanFacts `json:"query_plans,omitempty"`
	PlanID                  string                                                 `json:"plan_id"`
	// Strategy is the source identity only. The revision and the Python
	// snapshot revision that StrategyRefV2 also carries are output context.
	Strategy        contract.StrategyRefV2          `json:"strategy"`
	InputProjection contract.InputProjectionV2      `json:"input_projection"`
	OutputIdentity  *contract.MonitorOutputIdentity `json:"output_identity,omitempty"`
	TargetScope     *contract.TargetScopeV2         `json:"target_scope,omitempty"`
	// TargetPlan is the target's second frozen form. It is execution content
	// like TargetScope, and it is what moves an object onto the v2 contract:
	// a reader that does not know the field would otherwise run the Plan on
	// no target at all.
	TargetPlan *contract.TargetPlanV1 `json:"target_plan,omitempty"`
	// NoData is execution content: absence is judged while the Slot runs, and
	// Continuous is the trigger window the synthetic series is read with. It is
	// omitted when the item does not detect no-data, which keeps the digest of
	// every Plan that does not off this field - see the placement test.
	NoData             *contract.NoDataConfigV1 `json:"no_data,omitempty"`
	StrategyIR         contract.StrategyIRV2    `json:"strategy_ir"`
	TerminalReasonCode string                   `json:"terminal_reason_code,omitempty"`
	// Shard is the piece of a split strategy this Plan is; omitted for a
	// Plan that is not split, so no object of an unsplit Plan changes bytes.
	Shard *execution.ShardRef `json:"shard,omitempty"`
}

// Key is this object's Plan key: the strategy and the piece.
func (plan QueryGroupPlanObject) Key() execution.PlanKey {
	return execution.PlanKeyOf(plan.Identity, execution.ShardOf(plan.Shard))
}

// OutputContextObject is what event rendering reads for one Plan: the source
// identity with its revisions, the compatibility context, the subject facts
// and the wire format. None of it decides what a Worker evaluates.
//
// WireFormat is a deployment fact rather than a strategy fact, and it is
// frozen here rather than resolved when an event is emitted so that a Slot
// retried across a protocol switch publishes the same bytes both times. The
// price is that a protocol switch moves every Plan's OutputContextDigest at
// once and the Control Leader rewrites every output context object in one
// publication; it moves no ObjectDigest, so it cuts no Segment.
type OutputContextObject struct {
	ContractVersion     string                          `json:"output_context_contract_version"`
	Identity            execution.PlanIdentity          `json:"plan_identity"`
	StrategyRef         contract.StrategyRefV2          `json:"strategy_ref"`
	SourceCompatibility *contract.SourceCompatibilityV2 `json:"source_compatibility,omitempty"`
	SubjectFacts        *contract.MonitorSubjectFacts   `json:"subject_facts,omitempty"`
	LegacyOutput        *contract.LegacyOutputContext   `json:"legacy_output,omitempty"`
	WireFormat          string                          `json:"wire_format,omitempty"`
	// SignalType is here for the same reason WireFormat is: it is read only
	// when an event is rendered, and it is frozen with the Plan so a Slot
	// retried across a strategy edit publishes the same bytes both times.
	SignalType string `json:"signal_type,omitempty"`
}

// BuildQueryGroupObject projects the execution content out of a published
// Query Group. It copies nothing it does not keep, so the object never aliases
// the source document.
func BuildQueryGroupObject(group QueryGroup) QueryGroupObject {
	object := QueryGroupObject{
		Identity:         group.Identity,
		ScheduleRevision: group.ScheduleRevision, QueryPlan: group.QueryPlan,
		MembershipDigest: group.MembershipDigest, Plans: make([]QueryGroupPlanObject, 0, len(group.Plans)),
	}
	for _, plan := range group.Plans {
		object.Plans = append(object.Plans, buildQueryGroupPlanObject(plan))
	}
	object.ContractVersion = queryGroupObjectVersion(object.Plans)
	return object
}

// LevelContractRefObject is one Level contract reference as the object
// stores it. A type of its own rather than the execution type because that
// type's canonical encoding is the series warmup digest's input
// (alarmd-series-warmup-requirement-v1): tagging its fields for the object
// would move every stored series guard.
type LevelContractRefObject struct {
	LevelID                 uint32 `json:"level_id"`
	LevelStateCompatibility string `json:"level_state_compatibility"`
	WarmupRequirementRef    string `json:"warmup_requirement_ref"`
	DetectFingerprint       string `json:"detect_fingerprint"`
}

func levelContractRefObjects(refs []execution.RuntimeLevelContractRef) []LevelContractRefObject {
	if len(refs) == 0 {
		return nil
	}
	objects := make([]LevelContractRefObject, len(refs))
	for index, ref := range refs {
		objects[index] = LevelContractRefObject{LevelID: ref.LevelID, LevelStateCompatibility: ref.LevelStateCompatibility,
			WarmupRequirementRef: ref.WarmupRequirementRef, DetectFingerprint: ref.DetectFingerprint}
	}
	return objects
}

func levelContractRefsOf(objects []LevelContractRefObject) []execution.RuntimeLevelContractRef {
	if len(objects) == 0 {
		return nil
	}
	refs := make([]execution.RuntimeLevelContractRef, len(objects))
	for index, object := range objects {
		refs[index] = execution.RuntimeLevelContractRef{LevelID: object.LevelID, LevelStateCompatibility: object.LevelStateCompatibility,
			WarmupRequirementRef: object.WarmupRequirementRef, DetectFingerprint: object.DetectFingerprint}
	}
	return refs
}

func buildQueryGroupPlanObject(plan FrozenPlan) QueryGroupPlanObject {
	strategyIR := plan.Plan.StrategyIR
	strategyIR.StrategyRef = strategyIdentity(strategyIR.StrategyRef)
	return QueryGroupPlanObject{
		Identity: plan.Identity, StateGeneration: plan.StateGeneration, LevelContractRefs: levelContractRefObjects(plan.LevelContractRefs),
		NoDataLevelContractRefs: levelContractRefObjects(plan.NoDataLevelContractRefs),
		ScheduleSpec:            plan.ScheduleSpec, ScheduleRevision: plan.ScheduleRevision,
		RequirementTemplates: plan.RequirementTemplates, QueryPlans: plan.QueryPlans,
		PlanID: plan.Plan.PlanID, Strategy: strategyIdentity(plan.Plan.StrategyRef),
		InputProjection: plan.Plan.InputProjection, OutputIdentity: plan.Plan.OutputIdentity,
		TargetScope: plan.Plan.TargetScope, TargetPlan: plan.Plan.TargetPlan, NoData: plan.Plan.NoData, StrategyIR: strategyIR,
		EffectiveTimeSnapshot: append(json.RawMessage(nil), plan.Plan.EffectiveTimeSnapshot...),
		TerminalReasonCode:    plan.Plan.TerminalReasonCode,
		Shard:                 plan.Shard,
	}
}

// strategyIdentity keeps the fields of a StrategyRefV2 that name the strategy
// and drops the two that name a version of it.
func strategyIdentity(ref contract.StrategyRefV2) contract.StrategyRefV2 {
	return contract.StrategyRefV2{TenantID: ref.TenantID, StrategyID: ref.StrategyID}
}

// DeriveQueryGroupObjectDigest names the execution content of a Query Group.
// Two Query Groups with equal digests execute identically; a publication that
// leaves a Query Group's digest where it was has not changed that Query Group.
func DeriveQueryGroupObjectDigest(group QueryGroup) (execution.ObjectDigest, error) {
	object := BuildQueryGroupObject(group)
	digest, err := contract.DeriveCanonicalDigestV2(object.ContractVersion, object)
	return execution.ObjectDigest(digest), err
}

// BuildOutputContext projects the rendering context out of a published Plan.
func BuildOutputContext(plan FrozenPlan) OutputContextObject {
	return OutputContextObject{
		ContractVersion: outputContextContractVersion, Identity: plan.Identity,
		StrategyRef: plan.Plan.StrategyRef, SourceCompatibility: plan.Plan.SourceCompatibility,
		SubjectFacts: plan.Plan.SubjectFacts, LegacyOutput: plan.Plan.LegacyOutput,
		WireFormat: plan.Plan.WireFormat, SignalType: plan.Plan.SignalType,
	}
}

// DeriveOutputContextDigest names the rendering context of one Plan.
func DeriveOutputContextDigest(plan FrozenPlan) (execution.OutputContextDigest, error) {
	digest, err := contract.DeriveCanonicalDigestV2(outputContextContractVersion, BuildOutputContext(plan))
	return execution.OutputContextDigest(digest), err
}

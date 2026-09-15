// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
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
	outputContextContractVersion    = "alarmd-output-context-v1"
)

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
	Identity             execution.PlanIdentity                                 `json:"plan_identity"`
	StateGeneration      execution.StateGeneration                              `json:"state_generation,omitempty"`
	ScheduleSpec         execution.ScheduleSpec                                 `json:"schedule_spec"`
	ScheduleRevision     execution.PlanScheduleRevision                         `json:"plan_schedule_revision"`
	RequirementTemplates []execution.DataRequirementTemplate                    `json:"requirement_templates,omitempty"`
	QueryPlans           map[execution.LogicalQueryRef]execution.QueryPlanFacts `json:"query_plans,omitempty"`
	PlanID               string                                                 `json:"plan_id"`
	// Strategy is the source identity only. The revision and the Python
	// snapshot revision that StrategyRefV2 also carries are output context.
	Strategy           contract.StrategyRefV2          `json:"strategy"`
	InputProjection    contract.InputProjectionV2      `json:"input_projection"`
	OutputIdentity     *contract.MonitorOutputIdentity `json:"output_identity,omitempty"`
	TargetScope        *contract.TargetScopeV2         `json:"target_scope,omitempty"`
	StrategyIR         contract.StrategyIRV2           `json:"strategy_ir"`
	TerminalReasonCode string                          `json:"terminal_reason_code,omitempty"`
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
}

// BuildQueryGroupObject projects the execution content out of a published
// Query Group. It copies nothing it does not keep, so the object never aliases
// the source document.
func BuildQueryGroupObject(group QueryGroup) QueryGroupObject {
	object := QueryGroupObject{
		ContractVersion: queryGroupObjectContractVersion, Identity: group.Identity,
		ScheduleRevision: group.ScheduleRevision, QueryPlan: group.QueryPlan,
		MembershipDigest: group.MembershipDigest, Plans: make([]QueryGroupPlanObject, 0, len(group.Plans)),
	}
	for _, plan := range group.Plans {
		object.Plans = append(object.Plans, buildQueryGroupPlanObject(plan))
	}
	return object
}

func buildQueryGroupPlanObject(plan FrozenPlan) QueryGroupPlanObject {
	strategyIR := plan.Plan.StrategyIR
	strategyIR.StrategyRef = strategyIdentity(strategyIR.StrategyRef)
	return QueryGroupPlanObject{
		Identity: plan.Identity, StateGeneration: plan.StateGeneration,
		ScheduleSpec: plan.ScheduleSpec, ScheduleRevision: plan.ScheduleRevision,
		RequirementTemplates: plan.RequirementTemplates, QueryPlans: plan.QueryPlans,
		PlanID: plan.Plan.PlanID, Strategy: strategyIdentity(plan.Plan.StrategyRef),
		InputProjection: plan.Plan.InputProjection, OutputIdentity: plan.Plan.OutputIdentity,
		TargetScope: plan.Plan.TargetScope, StrategyIR: strategyIR,
		TerminalReasonCode: plan.Plan.TerminalReasonCode,
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
	digest, err := contract.DeriveCanonicalDigestV2(queryGroupObjectContractVersion, BuildQueryGroupObject(group))
	return execution.ObjectDigest(digest), err
}

// BuildOutputContext projects the rendering context out of a published Plan.
func BuildOutputContext(plan FrozenPlan) OutputContextObject {
	return OutputContextObject{
		ContractVersion: outputContextContractVersion, Identity: plan.Identity,
		StrategyRef: plan.Plan.StrategyRef, SourceCompatibility: plan.Plan.SourceCompatibility,
		SubjectFacts: plan.Plan.SubjectFacts, LegacyOutput: plan.Plan.LegacyOutput,
		WireFormat: plan.Plan.WireFormat,
	}
}

// DeriveOutputContextDigest names the rendering context of one Plan.
func DeriveOutputContextDigest(plan FrozenPlan) (execution.OutputContextDigest, error) {
	digest, err := contract.DeriveCanonicalDigestV2(outputContextContractVersion, BuildOutputContext(plan))
	return execution.OutputContextDigest(digest), err
}

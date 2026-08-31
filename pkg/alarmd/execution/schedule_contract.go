// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package execution

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type PublicationEpoch uint64

// ScheduleSpec is the complete cadence input for one Plan. Alignment is a
// resolved Unix-second grid anchor; Timezone remains part of the revision so
// source schedule semantics cannot change without a new lane.
type ScheduleSpec struct {
	EvaluationIntervalSeconds int64          `json:"evaluation_interval"`
	Alignment                 EvaluationTime `json:"alignment"`
	Timezone                  string         `json:"timezone"`
}

func (spec ScheduleSpec) Validate() error {
	if spec.EvaluationIntervalSeconds <= 0 {
		return errors.New("alarmd execution: positive evaluation interval is required")
	}
	if spec.Alignment < 0 {
		return errors.New("alarmd execution: schedule alignment cannot be negative")
	}
	if spec.Timezone == "" {
		return errors.New("alarmd execution: schedule timezone is required")
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		return fmt.Errorf("alarmd execution: invalid schedule timezone: %w", err)
	}
	return nil
}

func (spec ScheduleSpec) IsAligned(at EvaluationTime) bool {
	if spec.Validate() != nil || at <= 0 || at < spec.Alignment {
		return false
	}
	return (int64(at)-int64(spec.Alignment))%spec.EvaluationIntervalSeconds == 0
}

func (spec ScheduleSpec) nextAtOrAfter(at EvaluationTime) (EvaluationTime, bool) {
	if spec.Validate() != nil || at <= 0 {
		return 0, false
	}
	if at <= spec.Alignment {
		if spec.Alignment <= 0 {
			return EvaluationTime(spec.EvaluationIntervalSeconds), true
		}
		return spec.Alignment, true
	}
	delta := int64(at) - int64(spec.Alignment)
	remainder := delta % spec.EvaluationIntervalSeconds
	if remainder == 0 {
		return at, true
	}
	increment := spec.EvaluationIntervalSeconds - remainder
	if int64(at) > math.MaxInt64-increment {
		return 0, false
	}
	return EvaluationTime(int64(at) + increment), true
}

func DerivePlanScheduleRevision(spec ScheduleSpec) (PlanScheduleRevision, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-plan-schedule-v1", spec)
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive Plan schedule revision: %w", err)
	}
	return PlanScheduleRevision(digest), nil
}

type FrozenPlanSchedule struct {
	Identity         PlanIdentity
	ScheduleRevision PlanScheduleRevision
	Spec             ScheduleSpec
}

func (schedule FrozenPlanSchedule) Validate() error {
	if err := schedule.Identity.Validate(); err != nil {
		return err
	}
	revision, err := DerivePlanScheduleRevision(schedule.Spec)
	if err != nil {
		return err
	}
	if schedule.ScheduleRevision == "" || schedule.ScheduleRevision != revision {
		return errors.New("alarmd execution: Plan schedule revision does not match ScheduleSpec")
	}
	return nil
}

type FrozenPlanScheduleRef struct {
	Identity         PlanIdentity
	ScheduleRevision PlanScheduleRevision
}

func (ref FrozenPlanScheduleRef) Validate() error {
	if err := ref.Identity.Validate(); err != nil {
		return err
	}
	if ref.ScheduleRevision == "" {
		return errors.New("alarmd execution: Plan schedule revision is required")
	}
	return nil
}

func DeriveQueryGroupScheduleRevision(plans []FrozenPlanSchedule) (ScheduleRevision, error) {
	if len(plans) == 0 {
		return "", errors.New("alarmd execution: at least one active Plan schedule is required")
	}
	entries := make([]struct {
		Identity PlanIdentity
		Revision PlanScheduleRevision
	}, len(plans))
	seen := make(map[PlanIdentity]struct{}, len(plans))
	for index, plan := range plans {
		if err := plan.Validate(); err != nil {
			return "", err
		}
		if _, duplicate := seen[plan.Identity]; duplicate {
			return "", errors.New("alarmd execution: duplicate Plan schedule")
		}
		seen[plan.Identity] = struct{}{}
		entries[index] = struct {
			Identity PlanIdentity
			Revision PlanScheduleRevision
		}{Identity: plan.Identity, Revision: plan.ScheduleRevision}
	}
	sort.Slice(entries, func(left, right int) bool {
		return lessPlanIdentity(entries[left].Identity, entries[right].Identity)
	})
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-query-group-schedule-v1", entries)
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive Query Group schedule revision: %w", err)
	}
	return ScheduleRevision(digest), nil
}

// ScheduleLaneIdentity keeps Progress and Slot enumeration independent for
// each Query Group schedule revision.
type ScheduleLaneIdentity struct {
	QueryGroup       QueryGroupIdentity
	ScheduleRevision ScheduleRevision
}

func (lane ScheduleLaneIdentity) Validate() error {
	if lane.QueryGroup == "" || lane.ScheduleRevision == "" {
		return errors.New("alarmd execution: complete schedule lane identity is required")
	}
	return nil
}

func (lane ScheduleLaneIdentity) ProgressNamespace() ProgressNamespace {
	return ProgressNamespace{QueryGroup: lane.QueryGroup, ScheduleRevision: lane.ScheduleRevision}
}

// FrozenQueryGroupSchedule contains only immutable facts needed to enumerate
// one versioned schedule lane. FirstEvaluationTime is persisted control-plane
// fact, never a Worker first-seen timestamp.
type FrozenQueryGroupSchedule struct {
	Lane                ScheduleLaneIdentity
	FirstEvaluationTime EvaluationTime
	Plans               []FrozenPlanSchedule
}

func (schedule FrozenQueryGroupSchedule) Validate() error {
	if err := schedule.Lane.Validate(); err != nil {
		return err
	}
	if schedule.FirstEvaluationTime <= 0 {
		return errors.New("alarmd execution: positive first evaluation time is required")
	}
	revision, err := DeriveQueryGroupScheduleRevision(schedule.Plans)
	if err != nil {
		return err
	}
	if revision != schedule.Lane.ScheduleRevision {
		return errors.New("alarmd execution: lane revision does not match active Plan schedules")
	}
	if len(schedule.DuePlanRefs(schedule.FirstEvaluationTime)) == 0 {
		return errors.New("alarmd execution: first evaluation time is outside the schedule")
	}
	return nil
}

func (schedule FrozenQueryGroupSchedule) DuePlanRefs(at EvaluationTime) []FrozenPlanScheduleRef {
	if at < schedule.FirstEvaluationTime {
		return nil
	}
	due := make([]FrozenPlanScheduleRef, 0, len(schedule.Plans))
	for _, plan := range schedule.Plans {
		if plan.Spec.IsAligned(at) {
			due = append(due, FrozenPlanScheduleRef{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision})
		}
	}
	sort.Slice(due, func(left, right int) bool {
		return lessPlanIdentity(due[left].Identity, due[right].Identity)
	})
	return due
}

func (schedule FrozenQueryGroupSchedule) NextSlotAfter(current EvaluationTime) (EvaluationTime, bool) {
	if current >= EvaluationTime(math.MaxInt64) {
		return 0, false
	}
	next := EvaluationTime(math.MaxInt64)
	for _, plan := range schedule.Plans {
		candidate, ok := plan.Spec.nextAtOrAfter(current + 1)
		if ok && candidate < next {
			next = candidate
		}
	}
	return next, next != EvaluationTime(math.MaxInt64)
}

type SnapshotPublicationRef struct {
	SnapshotRevision SnapshotRevision
	PublicationEpoch PublicationEpoch
}

func (ref SnapshotPublicationRef) Validate() error {
	if ref.SnapshotRevision == "" || ref.PublicationEpoch == 0 {
		return errors.New("alarmd execution: complete content-addressed Snapshot publication is required")
	}
	return nil
}

// ScheduleCutoverFact is the persisted selection boundary between immutable
// publications and schedule lanes. It is separate from PlanActivationFact,
// which protects current side effects for one PlanIdentity.
type ScheduleCutoverFact struct {
	OldPublication        SnapshotPublicationRef
	NewPublication        SnapshotPublicationRef
	OldLane               ScheduleLaneIdentity
	NewLane               ScheduleLaneIdentity
	FirstEvaluationTime   EvaluationTime
	CutoverEvaluationTime EvaluationTime
}

func (fact ScheduleCutoverFact) Validate(oldSchedule, newSchedule FrozenQueryGroupSchedule) error {
	if err := fact.OldPublication.Validate(); err != nil {
		return err
	}
	if err := fact.NewPublication.Validate(); err != nil {
		return err
	}
	if fact.NewPublication.PublicationEpoch <= fact.OldPublication.PublicationEpoch ||
		fact.NewPublication.SnapshotRevision == fact.OldPublication.SnapshotRevision {
		return errors.New("alarmd execution: cutover publication must advance content and epoch")
	}
	if err := fact.OldLane.Validate(); err != nil {
		return err
	}
	if err := fact.NewLane.Validate(); err != nil {
		return err
	}
	if err := oldSchedule.Validate(); err != nil {
		return err
	}
	if err := newSchedule.Validate(); err != nil {
		return err
	}
	if fact.OldLane != oldSchedule.Lane || fact.NewLane != newSchedule.Lane {
		return errors.New("alarmd execution: cutover lanes do not match frozen schedules")
	}
	if fact.FirstEvaluationTime <= 0 || fact.CutoverEvaluationTime <= 0 ||
		fact.FirstEvaluationTime != newSchedule.FirstEvaluationTime || fact.CutoverEvaluationTime < fact.FirstEvaluationTime {
		return errors.New("alarmd execution: invalid persisted cutover times")
	}
	if len(newSchedule.DuePlanRefs(fact.FirstEvaluationTime)) == 0 || len(newSchedule.DuePlanRefs(fact.CutoverEvaluationTime)) == 0 {
		return errors.New("alarmd execution: persisted cutover times are outside the new schedule")
	}
	return nil
}

// FreezeSlotContractRequest is the exact 02-to-01 freeze request. It contains
// no latest selector, local time, ownership, assignment or execution identity.
type FreezeSlotContractRequest struct {
	Lane           ScheduleLaneIdentity
	EvaluationTime EvaluationTime
	DuePlans       []FrozenPlanScheduleRef
}

func (request FreezeSlotContractRequest) Validate() error {
	if err := request.Lane.Validate(); err != nil {
		return err
	}
	if request.EvaluationTime <= 0 || len(request.DuePlans) == 0 {
		return errors.New("alarmd execution: positive evaluation time and exact due Plan refs are required")
	}
	seen := make(map[PlanIdentity]struct{}, len(request.DuePlans))
	for _, due := range request.DuePlans {
		if err := due.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[due.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate exact due Plan ref")
		}
		seen[due.Identity] = struct{}{}
	}
	return nil
}

// FrozenSlotContractFact returns the full due digest inputs so Scheduler can
// independently verify the Catalog result instead of trusting an echoed hash.
type FrozenSlotContractFact struct {
	Contract     FrozenExecutionContractRef
	DuePlans     []DuePlan
	Requirements []DataRequirement
}

func (fact FrozenSlotContractFact) DeriveDuePlanSetDigest() (DuePlanSetDigest, error) {
	return DeriveDuePlanSetDigest(fact.DuePlans, fact.Requirements)
}

func (fact FrozenSlotContractFact) Validate(request FreezeSlotContractRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := fact.Contract.Validate(); err != nil {
		return err
	}
	if fact.Contract.Slot.QueryGroup != request.Lane.QueryGroup ||
		fact.Contract.Slot.ScheduleRevision != request.Lane.ScheduleRevision ||
		fact.Contract.ScheduleRevision != request.Lane.ScheduleRevision ||
		fact.Contract.Slot.EvaluationTime != request.EvaluationTime {
		return errors.New("alarmd execution: frozen Slot contract differs from requested lane or time")
	}
	if len(fact.DuePlans) != len(request.DuePlans) {
		return errors.New("alarmd execution: frozen due Plan cardinality differs from exact request")
	}
	wanted := make(map[PlanIdentity]PlanScheduleRevision, len(request.DuePlans))
	for _, due := range request.DuePlans {
		wanted[due.Identity] = due.ScheduleRevision
	}
	seen := make(map[PlanIdentity]struct{}, len(fact.DuePlans))
	for _, due := range fact.DuePlans {
		if err := due.Identity.Validate(); err != nil {
			return err
		}
		revision, ok := wanted[due.Identity]
		if !ok || revision != due.ScheduleRevision {
			return errors.New("alarmd execution: frozen due Plan facts differ from exact request")
		}
		if _, duplicate := seen[due.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate frozen due Plan fact")
		}
		seen[due.Identity] = struct{}{}
	}
	digest, err := fact.DeriveDuePlanSetDigest()
	if err != nil {
		return err
	}
	if digest != fact.Contract.DuePlanSetDigest {
		return errors.New("alarmd execution: frozen due Plan digest cannot be independently reproduced")
	}
	return nil
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

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

// ScheduleSpec is the complete immutable cadence input for one Plan.
type ScheduleSpec struct {
	EvaluationIntervalSeconds int64          `json:"evaluation_interval"`
	Alignment                 EvaluationTime `json:"alignment"`
	Timezone                  string         `json:"timezone"`
}

func (spec ScheduleSpec) Validate() error {
	if spec.EvaluationIntervalSeconds <= 0 {
		return errors.New("alarmd execution: positive evaluation interval is required")
	}
	if spec.Alignment < 0 || int64(spec.Alignment) >= spec.EvaluationIntervalSeconds {
		return errors.New("alarmd execution: schedule alignment must be within one evaluation interval")
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

func (spec ScheduleSpec) completionDeadlineUnixMilli(at EvaluationTime) (int64, bool) {
	if spec.Validate() != nil || at <= 0 || int64(at) > math.MaxInt64-spec.EvaluationIntervalSeconds {
		return 0, false
	}
	deadlineSeconds := int64(at) + spec.EvaluationIntervalSeconds
	if deadlineSeconds > math.MaxInt64/1000 {
		return 0, false
	}
	return deadlineSeconds * 1000, true
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

// ScheduleSegmentFact gives one schedule revision the half-open ownership
// interval [Start, End) on a Query Group's single logical timeline.
type ScheduleSegmentFact struct {
	Publication      SnapshotPublicationRef
	QueryGroup       QueryGroupIdentity
	QueryRevision    QueryRevision
	ScheduleRevision ScheduleRevision
	Start            EvaluationTime
	End              *EvaluationTime
}

func (segment ScheduleSegmentFact) Validate() error {
	if err := segment.Publication.Validate(); err != nil {
		return err
	}
	if segment.QueryGroup == "" || segment.QueryRevision == "" || segment.ScheduleRevision == "" || segment.Start <= 0 {
		return errors.New("alarmd execution: complete Schedule Segment is required")
	}
	if segment.End != nil && *segment.End <= segment.Start {
		return errors.New("alarmd execution: Schedule Segment end must follow its start")
	}
	return nil
}

func (segment ScheduleSegmentFact) Contains(at EvaluationTime) bool {
	return at >= segment.Start && (segment.End == nil || at < *segment.End)
}

func sameScheduleSegment(left, right ScheduleSegmentFact) bool {
	if left.Publication != right.Publication || left.QueryGroup != right.QueryGroup ||
		left.QueryRevision != right.QueryRevision || left.ScheduleRevision != right.ScheduleRevision || left.Start != right.Start {
		return false
	}
	if left.End == nil || right.End == nil {
		return left.End == nil && right.End == nil
	}
	return *left.End == *right.End
}

// FrozenQueryGroupSchedule binds one immutable Plan schedule set to the exact
// persisted Segment that owns its Slot times.
type FrozenQueryGroupSchedule struct {
	Segment ScheduleSegmentFact
	Plans   []FrozenPlanSchedule
}

func (schedule FrozenQueryGroupSchedule) Validate() error {
	if err := schedule.Segment.Validate(); err != nil {
		return err
	}
	revision, err := DeriveQueryGroupScheduleRevision(schedule.Plans)
	if err != nil {
		return err
	}
	if revision != schedule.Segment.ScheduleRevision {
		return errors.New("alarmd execution: Segment revision does not match active Plan schedules")
	}
	if _, ok := schedule.FirstSlot(); !ok {
		return errors.New("alarmd execution: Schedule Segment contains no legal Slot")
	}
	return nil
}

func (schedule FrozenQueryGroupSchedule) FirstSlot() (EvaluationTime, bool) {
	return schedule.nextSlotAtOrAfter(schedule.Segment.Start)
}

func (schedule FrozenQueryGroupSchedule) DuePlanRefs(at EvaluationTime) []FrozenPlanScheduleRef {
	if !schedule.Segment.Contains(at) {
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
	if !schedule.Segment.Contains(current) || current >= EvaluationTime(math.MaxInt64) {
		return 0, false
	}
	return schedule.nextSlotAtOrAfter(current + 1)
}

func (schedule FrozenQueryGroupSchedule) nextSlotAtOrAfter(at EvaluationTime) (EvaluationTime, bool) {
	if at < schedule.Segment.Start {
		at = schedule.Segment.Start
	}
	next := EvaluationTime(math.MaxInt64)
	for _, plan := range schedule.Plans {
		candidate, ok := plan.Spec.nextAtOrAfter(at)
		if ok && candidate < next {
			next = candidate
		}
	}
	return next, next != EvaluationTime(math.MaxInt64) && schedule.Segment.Contains(next)
}

// InitialScheduleActivationFact establishes the first open Segment without an
// invented old publication or a Worker-local first-seen time.
type InitialScheduleActivationFact struct {
	Segment ScheduleSegmentFact
}

func (fact InitialScheduleActivationFact) Validate(schedule FrozenQueryGroupSchedule) error {
	if err := fact.Segment.Validate(); err != nil {
		return err
	}
	if err := schedule.Validate(); err != nil {
		return err
	}
	if fact.Segment.End != nil || !sameScheduleSegment(fact.Segment, schedule.Segment) {
		return errors.New("alarmd execution: initial activation must establish the first open Schedule Segment")
	}
	return nil
}

// ScheduleCutoverFact atomically closes OldSegment and opens NewSegment at one
// boundary. It records immutable facts and does not introduce a state machine.
type ScheduleCutoverFact struct {
	OldSegment ScheduleSegmentFact
	NewSegment ScheduleSegmentFact
}

func (fact ScheduleCutoverFact) Validate(oldSchedule, newSchedule FrozenQueryGroupSchedule) error {
	if err := oldSchedule.Validate(); err != nil {
		return err
	}
	if err := newSchedule.Validate(); err != nil {
		return err
	}
	if !sameScheduleSegment(fact.OldSegment, oldSchedule.Segment) ||
		!sameScheduleSegment(fact.NewSegment, newSchedule.Segment) {
		return errors.New("alarmd execution: cutover Segments do not match frozen schedules")
	}
	if fact.OldSegment.QueryGroup != fact.NewSegment.QueryGroup || fact.OldSegment.End == nil ||
		*fact.OldSegment.End != fact.NewSegment.Start || fact.NewSegment.End != nil {
		return errors.New("alarmd execution: cutover requires adjacent half-open Segments on one Query Group timeline")
	}
	if fact.NewSegment.Publication.PublicationEpoch <= fact.OldSegment.Publication.PublicationEpoch ||
		fact.NewSegment.Publication.SnapshotRevision == fact.OldSegment.Publication.SnapshotRevision {
		return errors.New("alarmd execution: cutover publication must advance content and epoch")
	}
	return nil
}

// FreezeSlotContractRequest is the exact Scheduler-to-Catalog request. It has
// no latest selector, local time, ownership, assignment or execution identity.
type FreezeSlotContractRequest struct {
	QueryGroup           QueryGroupIdentity
	ScheduleRevision     ScheduleRevision
	ScheduleSegmentStart EvaluationTime
	EvaluationTime       EvaluationTime
	DuePlans             []FrozenPlanScheduleRef
}

func (request FreezeSlotContractRequest) Validate() error {
	if request.QueryGroup == "" || request.ScheduleRevision == "" || request.ScheduleSegmentStart <= 0 ||
		request.EvaluationTime < request.ScheduleSegmentStart || len(request.DuePlans) == 0 {
		return errors.New("alarmd execution: complete frozen Slot request is required")
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
// independently validate exact refs, ScheduleSpec/deadline and the digest.
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
	if fact.Contract.Slot.QueryGroup != request.QueryGroup ||
		fact.Contract.ScheduleRevision != request.ScheduleRevision ||
		fact.Contract.ScheduleSegmentStart != request.ScheduleSegmentStart ||
		fact.Contract.Slot.EvaluationTime != request.EvaluationTime {
		return errors.New("alarmd execution: frozen Slot contract differs from requested Segment or time")
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
		revision, err := DerivePlanScheduleRevision(due.ScheduleSpec)
		if err != nil {
			return err
		}
		deadline, ok := due.ScheduleSpec.completionDeadlineUnixMilli(request.EvaluationTime)
		if !ok || revision != due.ScheduleRevision || !due.ScheduleSpec.IsAligned(request.EvaluationTime) ||
			deadline != due.CompletionDeadlineUnixMilli {
			return errors.New("alarmd execution: frozen due Plan ScheduleSpec or deadline is invalid")
		}
		wantedRevision, exists := wanted[due.Identity]
		if !exists || wantedRevision != due.ScheduleRevision {
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

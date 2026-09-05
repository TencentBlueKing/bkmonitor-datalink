// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

// This file implements one approved administrative migration for two explicit
// legacy Draining facts. It is not a recovery path and must be deleted after
// the BKOP write/readback and rollback-window checks are complete.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const TemporaryLegacyDrainingCleanupSchemaVersion = "alarmd-temporary-legacy-draining-cleanup-v1"

var (
	ErrTemporaryLegacyDrainingCleanupConflict = errors.New("alarmd controlplane: temporary legacy Draining cleanup conflict")
	ErrTemporaryLegacyDrainingCleanupUnknown  = errors.New("alarmd controlplane: temporary legacy Draining cleanup result unknown")
)

type TemporaryLegacyDrainingTarget struct {
	QueryGroup       execution.QueryGroupIdentity `json:"query_group"`
	RetiredBoundary  execution.EvaluationTime     `json:"retired_boundary"`
	ProgressNextSlot execution.EvaluationTime     `json:"progress_next_slot"`
}

type TemporaryLegacyDrainingCleanupRequest struct {
	SchemaVersion              string                          `json:"schema_version"`
	ExpectedActivationRevision uint64                          `json:"expected_activation_revision"`
	ExpectedCurrent            SnapshotPublicationRef          `json:"expected_current"`
	ExpectedCandidate          SnapshotPublicationRef          `json:"expected_candidate"`
	ExpectedActiveQGSetRef     ActiveQueryGroupSetRef          `json:"expected_active_qg_set_ref"`
	CutoverBoundary            execution.EvaluationTime        `json:"cutover_boundary"`
	Targets                    []TemporaryLegacyDrainingTarget `json:"targets"`
}

type TemporaryLegacyDrainingProgressFact struct {
	RedisKey string
	Raw      []byte
	Load     execution.ProgressLoadResult
}

type TemporaryLegacyDrainingProgressReader interface {
	LoadTemporaryLegacyDrainingProgress(context.Context, execution.ProgressIdentity) (TemporaryLegacyDrainingProgressFact, error)
}

type TemporaryLegacyDrainingTargetPlan struct {
	QueryGroup         execution.QueryGroupIdentity `json:"query_group"`
	RetiredBoundary    execution.EvaluationTime     `json:"retired_boundary"`
	ProgressNextSlot   execution.EvaluationTime     `json:"progress_next_slot"`
	TimelineDigest     string                       `json:"timeline_digest"`
	ProgressDigest     string                       `json:"progress_digest"`
	CompletedWatermark execution.EvaluationTime     `json:"completed_watermark"`
}

type TemporaryLegacyDrainingCleanupPlan struct {
	SchemaVersion            string                              `json:"schema_version"`
	Digest                   string                              `json:"digest,omitempty"`
	ActivationDigest         string                              `json:"activation_digest"`
	NextActivationDigest     string                              `json:"next_activation_digest"`
	ScheduleTransitionDigest string                              `json:"schedule_transition_digest"`
	Current                  SnapshotPublicationRef              `json:"current"`
	Candidate                SnapshotPublicationRef              `json:"candidate"`
	ActivationRevision       uint64                              `json:"activation_revision"`
	CurrentActiveQGSet       ActiveQueryGroupSetRef              `json:"current_active_qg_set"`
	NextActiveQGSet          ActiveQueryGroupSetRef              `json:"next_active_qg_set"`
	CutoverBoundary          execution.EvaluationTime            `json:"cutover_boundary"`
	CandidateQueryGroups     uint64                              `json:"candidate_query_groups"`
	Targets                  []TemporaryLegacyDrainingTargetPlan `json:"targets"`
}

type TemporaryLegacyDrainingCleanupApplyStatus string

const (
	TemporaryLegacyDrainingCleanupApplied           TemporaryLegacyDrainingCleanupApplyStatus = "APPLIED"
	TemporaryLegacyDrainingCleanupConflictZeroWrite TemporaryLegacyDrainingCleanupApplyStatus = "CONFLICT_ZERO_WRITE"
	TemporaryLegacyDrainingCleanupUnknownApplied    TemporaryLegacyDrainingCleanupApplyStatus = "UNKNOWN_APPLIED"
	TemporaryLegacyDrainingCleanupUnknownZeroWrite  TemporaryLegacyDrainingCleanupApplyStatus = "UNKNOWN_ZERO_WRITE"
	TemporaryLegacyDrainingCleanupUnknownState      TemporaryLegacyDrainingCleanupApplyStatus = "UNKNOWN_STATE"
)

type TemporaryLegacyDrainingCleanupApplyResult struct {
	Status TemporaryLegacyDrainingCleanupApplyStatus `json:"status"`
	Plan   TemporaryLegacyDrainingCleanupPlan        `json:"plan"`
}

type temporaryLegacyDrainingCleanupEvalError struct{ err error }

func (failure *temporaryLegacyDrainingCleanupEvalError) Error() string { return failure.err.Error() }
func (failure *temporaryLegacyDrainingCleanupEvalError) Unwrap() error { return failure.err }

type TemporaryLegacyDrainingCleanup struct {
	repository     *RedisCatalogRepository
	compiler       RuntimePlanCompiler
	stateSemantics strategy.StateSemantics
	progress       TemporaryLegacyDrainingProgressReader
}

type temporaryLegacyDrainingPrepared struct {
	plan                        TemporaryLegacyDrainingCleanupPlan
	expectedHeader              string
	nextHeader                  string
	expectedActivationRaw       []byte
	nextActivationRaw           []byte
	expectedCurrentActiveRaw    []byte
	expectedLatest              string
	expectedCandidateSnapshot   []byte
	expectedCandidateEpoch      string
	expectedCandidateOccurrence string
	nextActivePayload           []byte
	nextActiveExisting          []byte
	nextActiveMissing           bool
	updates                     []scheduleTimelineUpdate
	progressFacts               []TemporaryLegacyDrainingProgressFact
}

func NewTemporaryLegacyDrainingCleanup(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	progress TemporaryLegacyDrainingProgressReader,
) (*TemporaryLegacyDrainingCleanup, error) {
	if repository == nil || compiler == nil || !validStateSemantics(stateSemantics) || progress == nil {
		return nil, errors.New("alarmd controlplane: temporary legacy Draining cleanup dependencies are incomplete")
	}
	return &TemporaryLegacyDrainingCleanup{repository: repository, compiler: compiler, stateSemantics: stateSemantics, progress: progress}, nil
}

func (cleanup *TemporaryLegacyDrainingCleanup) DryRun(
	ctx context.Context,
	request TemporaryLegacyDrainingCleanupRequest,
) (TemporaryLegacyDrainingCleanupPlan, error) {
	prepared, err := cleanup.prepare(ctx, request)
	if err != nil {
		return TemporaryLegacyDrainingCleanupPlan{}, err
	}
	return prepared.plan, nil
}

func (cleanup *TemporaryLegacyDrainingCleanup) Apply(
	ctx context.Context,
	request TemporaryLegacyDrainingCleanupRequest,
	expectedPlanDigest string,
) (TemporaryLegacyDrainingCleanupApplyResult, error) {
	prepared, err := cleanup.prepare(ctx, request)
	if err != nil {
		return TemporaryLegacyDrainingCleanupApplyResult{}, err
	}
	result := TemporaryLegacyDrainingCleanupApplyResult{Plan: prepared.plan}
	if len(expectedPlanDigest) != 64 || expectedPlanDigest != prepared.plan.Digest {
		result.Status = TemporaryLegacyDrainingCleanupConflictZeroWrite
		return result, ErrTemporaryLegacyDrainingCleanupConflict
	}
	changed, err := cleanup.persist(ctx, prepared)
	if err == nil {
		if changed != 1 {
			result.Status = TemporaryLegacyDrainingCleanupConflictZeroWrite
			return result, ErrTemporaryLegacyDrainingCleanupConflict
		}
		result.Status = TemporaryLegacyDrainingCleanupApplied
		return result, nil
	}
	var evalFailure *temporaryLegacyDrainingCleanupEvalError
	if !errors.As(err, &evalFailure) {
		result.Status = TemporaryLegacyDrainingCleanupConflictZeroWrite
		return result, err
	}
	result.Status = cleanup.readBackStatus(ctx, prepared)
	return result, fmt.Errorf("%w: %v", ErrTemporaryLegacyDrainingCleanupUnknown, err)
}

func (cleanup *TemporaryLegacyDrainingCleanup) prepare(
	ctx context.Context,
	request TemporaryLegacyDrainingCleanupRequest,
) (temporaryLegacyDrainingPrepared, error) {
	if cleanup == nil || cleanup.repository == nil || cleanup.progress == nil {
		return temporaryLegacyDrainingPrepared{}, errors.New("alarmd controlplane: temporary legacy Draining cleanup is required")
	}
	if err := validateTemporaryLegacyDrainingCleanupRequest(request); err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	repository := cleanup.repository
	activationRaw, err := repository.client.Get(ctx, repository.activationKey()).Bytes()
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, activationDependencyIO(err)
	}
	activation, err := repository.LoadActivation(ctx)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	if activation.SchemaVersion != activationSchemaVersion || activation.RecordRevision != request.ExpectedActivationRevision ||
		activation.Current != request.ExpectedCurrent || activation.Pending != nil || activation.ActiveQGSetRef != request.ExpectedActiveQGSetRef {
		return temporaryLegacyDrainingPrepared{}, ErrTemporaryLegacyDrainingCleanupConflict
	}
	expectedHeader, err := activationHeader(activation.RecordRevision, activation.Current, activation.Pending)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	latest, err := repository.LoadLatestPublication(ctx)
	if err != nil || latest != request.ExpectedCandidate {
		if err != nil {
			return temporaryLegacyDrainingPrepared{}, err
		}
		return temporaryLegacyDrainingPrepared{}, ErrTemporaryLegacyDrainingCleanupConflict
	}
	candidateSnapshot, err := repository.LoadPublishedSnapshot(ctx, request.ExpectedCandidate)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	candidateSnapshotRaw, err := repository.client.Get(ctx, repository.snapshotKey(request.ExpectedCandidate.SnapshotRevision)).Bytes()
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, activationDependencyIO(err)
	}
	currentActiveRaw, err := repository.client.Get(ctx, repository.activeQGSetKey(activation.ActiveQGSetRef.Digest)).Bytes()
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, activationDependencyIO(err)
	}
	currentActive, err := repository.LoadActiveQueryGroupSet(ctx, activation.ActiveQGSetRef)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	candidateGroups, err := queryGroupMap(candidateSnapshot.QueryGroups)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	oldGroups, err := cleanup.loadCurrentGroups(ctx, activation, currentActive)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	targetByGroup := make(map[execution.QueryGroupIdentity]TemporaryLegacyDrainingTarget, len(request.Targets))
	for _, target := range request.Targets {
		targetByGroup[target.QueryGroup] = target
		if _, exists := candidateGroups[target.QueryGroup]; !exists {
			return temporaryLegacyDrainingPrepared{}, ErrTemporaryLegacyDrainingCleanupConflict
		}
	}
	drainingByGroup := make(map[execution.QueryGroupIdentity]DrainingQueryGroup, len(activation.Draining))
	for _, draining := range activation.Draining {
		drainingByGroup[draining.QueryGroup] = draining
	}
	for identity, target := range targetByGroup {
		if drainingByGroup[identity].RetiredBoundary != target.RetiredBoundary {
			return temporaryLegacyDrainingPrepared{}, ErrTemporaryLegacyDrainingCleanupConflict
		}
	}

	records, _, err := compilePublishedActivation(ctx, cleanup.compiler, cleanup.stateSemantics, candidateSnapshot, request.CutoverBoundary)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	previousRecords, err := activationRecordMap(activation.Plans)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	planGroup := make(map[execution.PlanIdentity]execution.QueryGroupIdentity)
	for _, group := range candidateSnapshot.QueryGroups {
		for _, plan := range group.Plans {
			planGroup[plan.Identity] = group.Identity
		}
	}
	for index := range records {
		previous, continuous := previousRecords[records[index].Fact.Plan]
		_, target := targetByGroup[planGroup[records[index].Fact.Plan]]
		if target || !continuous || previous.Fact.Selected.StateGeneration != records[index].Fact.Selected.StateGeneration {
			records[index].Fact.Selected.ForceWarming = true
		}
	}
	sort.Slice(records, func(i, j int) bool { return lessPlanIdentity(records[i].Fact.Plan, records[j].Fact.Plan) })

	reactivating := make(map[execution.QueryGroupIdentity]struct{})
	for _, draining := range activation.Draining {
		if _, present := candidateGroups[draining.QueryGroup]; !present {
			continue
		}
		if _, target := targetByGroup[draining.QueryGroup]; !target {
			return temporaryLegacyDrainingPrepared{}, ErrTemporaryLegacyDrainingCleanupConflict
		}
		reactivating[draining.QueryGroup] = struct{}{}
	}
	nextDraining, err := expectedDrainingProjection(activation.Draining, oldGroups, candidateGroups, reactivating, request.CutoverBoundary)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	next := ActivationState{SchemaVersion: activationSchemaVersion, RecordRevision: activation.RecordRevision + 1,
		Current: request.ExpectedCandidate, Plans: records, Draining: nextDraining}

	updates, coverage, targetPlans, progressFacts, err := cleanup.buildScheduleUpdates(
		ctx, request, activation, next, candidateSnapshot, oldGroups, candidateGroups, reactivating, targetByGroup,
	)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	if len(coverage) == 0 && len(next.Plans) != 0 {
		return temporaryLegacyDrainingPrepared{}, errors.New("alarmd controlplane: empty temporary cleanup Schedule coverage")
	}
	if len(coverage) > 0 {
		if err := validateInitialActivationCoverage(next, coverage); err != nil {
			return temporaryLegacyDrainingPrepared{}, err
		}
	}
	activeIdentities := make([]execution.QueryGroupIdentity, 0, len(candidateGroups))
	for identity := range candidateGroups {
		activeIdentities = append(activeIdentities, identity)
	}
	nextActiveRef, nextActivePayload, err := canonicalActiveQueryGroupSet(activeIdentities)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	next.ActiveQGSetRef = nextActiveRef
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	nextActivationRaw, err := json.Marshal(next)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	nextActiveExisting, nextActiveMissing, err := repository.readOptionalRaw(ctx, repository.activeQGSetKey(nextActiveRef.Digest))
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	if !nextActiveMissing && string(nextActiveExisting) != string(nextActivePayload) {
		return temporaryLegacyDrainingPrepared{}, &ActiveQueryGroupSetConflictError{Err: errors.New("temporary cleanup target Active Set collision")}
	}
	scheduleTransitionDigest, err := digestTemporaryLegacyScheduleUpdates(updates)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	plan := TemporaryLegacyDrainingCleanupPlan{
		SchemaVersion:            TemporaryLegacyDrainingCleanupSchemaVersion,
		ActivationDigest:         digestBytes(activationRaw),
		NextActivationDigest:     digestBytes(nextActivationRaw),
		ScheduleTransitionDigest: scheduleTransitionDigest,
		Current:                  activation.Current,
		Candidate:                request.ExpectedCandidate,
		ActivationRevision:       activation.RecordRevision, CurrentActiveQGSet: activation.ActiveQGSetRef,
		NextActiveQGSet: nextActiveRef, CutoverBoundary: request.CutoverBoundary,
		CandidateQueryGroups: uint64(len(candidateGroups)), Targets: targetPlans,
	}
	plan.Digest, err = digestTemporaryLegacyDrainingPlan(plan)
	if err != nil {
		return temporaryLegacyDrainingPrepared{}, err
	}
	return temporaryLegacyDrainingPrepared{
		plan: plan, expectedHeader: expectedHeader, nextHeader: nextHeader,
		expectedActivationRaw: activationRaw, nextActivationRaw: nextActivationRaw,
		expectedCurrentActiveRaw: currentActiveRaw, expectedLatest: publicationValue(request.ExpectedCandidate),
		expectedCandidateSnapshot:   candidateSnapshotRaw,
		expectedCandidateEpoch:      strconv.FormatUint(request.ExpectedCandidate.PublicationEpoch, 10),
		expectedCandidateOccurrence: string(request.ExpectedCandidate.SnapshotRevision),
		nextActivePayload:           nextActivePayload, nextActiveExisting: nextActiveExisting, nextActiveMissing: nextActiveMissing,
		updates: updates, progressFacts: progressFacts,
	}, nil
}

func (cleanup *TemporaryLegacyDrainingCleanup) loadCurrentGroups(
	ctx context.Context,
	activation ActivationState,
	active []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]QueryGroup, error) {
	snapshot, err := cleanup.repository.LoadPublishedSnapshot(ctx, activation.Current)
	if err == nil {
		return queryGroupMap(snapshot.QueryGroups)
	}
	if !errors.Is(err, ErrSnapshotUnavailable) {
		return nil, err
	}
	candidates := make(map[execution.QueryGroupIdentity]QueryGroup, len(active))
	for _, identity := range active {
		candidates[identity] = QueryGroup{Identity: identity}
	}
	return cleanup.repository.loadActivatedGroupsFromOpenSchedules(ctx, activation, candidates)
}

func validateTemporaryLegacyDrainingCleanupRequest(request TemporaryLegacyDrainingCleanupRequest) error {
	if request.SchemaVersion != TemporaryLegacyDrainingCleanupSchemaVersion {
		return errors.New("alarmd controlplane: unsupported temporary legacy Draining cleanup schema")
	}
	if request.ExpectedActivationRevision == 0 || request.ExpectedCurrent.validate() != nil || request.ExpectedCandidate.validate() != nil ||
		request.ExpectedCurrent == request.ExpectedCandidate || request.ExpectedCandidate.PublicationEpoch <= request.ExpectedCurrent.PublicationEpoch ||
		request.ExpectedActiveQGSetRef.validate() != nil || request.CutoverBoundary <= 0 || len(request.Targets) != 2 {
		return errors.New("alarmd controlplane: invalid temporary legacy Draining cleanup request")
	}
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(request.Targets))
	for _, target := range request.Targets {
		if !completeQueryGroupIdentity(target.QueryGroup) || target.ProgressNextSlot <= 0 ||
			request.CutoverBoundary < target.RetiredBoundary || target.RetiredBoundary <= target.ProgressNextSlot {
			return errors.New("alarmd controlplane: invalid temporary legacy Draining target")
		}
		if _, duplicate := seen[target.QueryGroup]; duplicate {
			return errors.New("alarmd controlplane: duplicate temporary legacy Draining target")
		}
		seen[target.QueryGroup] = struct{}{}
	}
	return nil
}

func completeQueryGroupIdentity(identity execution.QueryGroupIdentity) bool {
	if len(identity) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(string(identity))
	return err == nil && len(decoded) == sha256.Size && string(identity) == strings.ToLower(string(identity))
}

func digestTemporaryLegacyDrainingPlan(plan TemporaryLegacyDrainingCleanupPlan) (string, error) {
	plan.Digest = ""
	payload, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func digestBytes(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func digestTemporaryLegacyScheduleUpdates(updates []scheduleTimelineUpdate) (string, error) {
	type transition struct {
		QueryGroup     execution.QueryGroupIdentity `json:"query_group"`
		ExpectedDigest string                       `json:"expected_digest"`
		NextDigest     string                       `json:"next_digest"`
	}
	values := make([]transition, 0, len(updates))
	for _, update := range updates {
		next, err := json.Marshal(update.next)
		if err != nil {
			return "", err
		}
		values = append(values, transition{
			QueryGroup: update.next.QueryGroup, ExpectedDigest: digestBytes(update.expected), NextDigest: digestBytes(next),
		})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].QueryGroup < values[j].QueryGroup })
	payload, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func (cleanup *TemporaryLegacyDrainingCleanup) buildScheduleUpdates(
	ctx context.Context,
	request TemporaryLegacyDrainingCleanupRequest,
	previous ActivationState,
	next ActivationState,
	candidate PublishedSnapshot,
	oldGroups map[execution.QueryGroupIdentity]QueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
	reactivating map[execution.QueryGroupIdentity]struct{},
	targets map[execution.QueryGroupIdentity]TemporaryLegacyDrainingTarget,
) ([]scheduleTimelineUpdate, []persistedScheduleTimeline, []TemporaryLegacyDrainingTargetPlan, []TemporaryLegacyDrainingProgressFact, error) {
	updates := make([]scheduleTimelineUpdate, 0, len(oldGroups)+len(newGroups))
	coverage := make([]persistedScheduleTimeline, 0, len(newGroups))
	targetPlans := make([]TemporaryLegacyDrainingTargetPlan, 0, len(targets))
	progressFacts := make([]TemporaryLegacyDrainingProgressFact, 0, len(targets))
	for queryGroup, oldGroup := range oldGroups {
		timeline, raw, err := cleanup.repository.loadScheduleTimeline(ctx, queryGroup)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		last := len(timeline.Segments) - 1
		if last < 0 || timeline.RetiredAt != nil {
			return nil, nil, nil, nil, ErrScheduleConflict
		}
		open := timeline.Segments[last]
		if open.Schedule.Segment.End != nil || open.Schedule.Segment.Start >= request.CutoverBoundary ||
			open.Schedule.Segment.Publication.SnapshotRevision != previous.Current.SnapshotRevision ||
			open.Schedule.Segment.Publication.PublicationEpoch != execution.PublicationEpoch(previous.Current.PublicationEpoch) ||
			open.Schedule.Segment.QueryRevision != oldGroup.QueryPlan.QueryRevision ||
			open.Schedule.Segment.ScheduleRevision != oldGroup.ScheduleRevision {
			return nil, nil, nil, nil, ErrScheduleConflict
		}
		if err := validateOpenSegmentActivation(previous, open); err != nil {
			return nil, nil, nil, nil, err
		}
		closed := open.Schedule
		closed.Segment.End = &request.CutoverBoundary
		if err := closed.Validate(); err != nil {
			return nil, nil, nil, nil, err
		}
		timeline.Segments[last].Schedule = closed
		timeline.RecordRevision++
		if newGroup, remains := newGroups[queryGroup]; remains {
			opened, records, err := cleanup.openCandidateSchedule(ctx, candidate, next, newGroup, request.CutoverBoundary)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			timeline.Segments = append(timeline.Segments, persistedScheduleSegment{Schedule: opened, Plans: records})
			coverage = append(coverage, coverageTimeline(queryGroup, opened, records))
		} else {
			retiredAt := request.CutoverBoundary
			timeline.RetiredAt = &retiredAt
		}
		if err := validateScheduleTimeline(timeline); err != nil {
			return nil, nil, nil, nil, err
		}
		updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
	}
	for queryGroup, newGroup := range newGroups {
		if _, existed := oldGroups[queryGroup]; existed {
			continue
		}
		opened, records, err := cleanup.openCandidateSchedule(ctx, candidate, next, newGroup, request.CutoverBoundary)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		var timeline persistedScheduleTimeline
		var raw []byte
		if _, reactivated := reactivating[queryGroup]; reactivated {
			timeline, raw, err = cleanup.repository.loadScheduleTimeline(ctx, queryGroup)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			retiredAt := *timeline.RetiredAt
			if target, administrative := targets[queryGroup]; administrative {
				fact, plan, factErr := cleanup.validateTargetProgress(ctx, timeline, raw, target, opened)
				if factErr != nil {
					return nil, nil, nil, nil, factErr
				}
				retiredAt = target.ProgressNextSlot
				last := len(timeline.Segments) - 1
				timeline.Segments[last].Schedule.Segment.End = &retiredAt
				timeline.RetiredAt = &retiredAt
				targetPlans = append(targetPlans, plan)
				progressFacts = append(progressFacts, fact)
			}
			if timeline.RetiredAt == nil || *timeline.RetiredAt >= request.CutoverBoundary {
				return nil, nil, nil, nil, ErrScheduleConflict
			}
			timeline.RetiredAt = nil
			timeline.RecordRevision++
			timeline.Segments = append(timeline.Segments, persistedScheduleSegment{
				Schedule: opened, Plans: records, ReactivatedAfter: &retiredAt,
			})
		} else {
			timeline = persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
				RecordRevision: 1, QueryGroup: queryGroup,
				Segments: []persistedScheduleSegment{{Schedule: opened, Plans: records}}}
		}
		if err := validateScheduleTimeline(timeline); err != nil {
			return nil, nil, nil, nil, err
		}
		updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
		coverage = append(coverage, coverageTimeline(queryGroup, opened, records))
	}
	if len(targetPlans) != len(targets) || len(progressFacts) != len(targets) {
		return nil, nil, nil, nil, ErrTemporaryLegacyDrainingCleanupConflict
	}
	sort.Slice(targetPlans, func(i, j int) bool { return targetPlans[i].QueryGroup < targetPlans[j].QueryGroup })
	sort.Slice(progressFacts, func(i, j int) bool {
		return progressFacts[i].Load.Progress.Identity.QueryGroup < progressFacts[j].Load.Progress.Identity.QueryGroup
	})
	return updates, coverage, targetPlans, progressFacts, nil
}

func (cleanup *TemporaryLegacyDrainingCleanup) openCandidateSchedule(
	ctx context.Context,
	candidate PublishedSnapshot,
	next ActivationState,
	group QueryGroup,
	boundary execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, []PlanActivationRecord, error) {
	opened, err := cleanup.repository.materializeSchedule(ctx, scheduleSegmentForGroup(candidate.Publication, group, boundary))
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, nil, err
	}
	first, ok := opened.FirstSlot()
	if !ok || first < boundary {
		return execution.FrozenQueryGroupSchedule{}, nil, errors.New("alarmd controlplane: temporary cleanup candidate has no legal Slot at or after cutover")
	}
	records, err := activationRecordsForSchedule(next, opened)
	return opened, records, err
}

func coverageTimeline(
	queryGroup execution.QueryGroupIdentity,
	schedule execution.FrozenQueryGroupSchedule,
	records []PlanActivationRecord,
) persistedScheduleTimeline {
	return persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion, RecordRevision: 1,
		QueryGroup: queryGroup, Segments: []persistedScheduleSegment{{Schedule: schedule, Plans: records}}}
}

func (cleanup *TemporaryLegacyDrainingCleanup) validateTargetProgress(
	ctx context.Context,
	timeline persistedScheduleTimeline,
	timelineRaw []byte,
	target TemporaryLegacyDrainingTarget,
	opened execution.FrozenQueryGroupSchedule,
) (TemporaryLegacyDrainingProgressFact, TemporaryLegacyDrainingTargetPlan, error) {
	if timeline.RetiredAt == nil || *timeline.RetiredAt != target.RetiredBoundary || len(timeline.Segments) == 0 {
		return TemporaryLegacyDrainingProgressFact{}, TemporaryLegacyDrainingTargetPlan{}, ErrTemporaryLegacyDrainingCleanupConflict
	}
	last := timeline.Segments[len(timeline.Segments)-1]
	if last.Schedule.Segment.End == nil || *last.Schedule.Segment.End != target.RetiredBoundary ||
		!last.Schedule.Segment.Contains(target.ProgressNextSlot) || len(last.Schedule.DuePlanRefs(target.ProgressNextSlot)) == 0 {
		return TemporaryLegacyDrainingProgressFact{}, TemporaryLegacyDrainingTargetPlan{}, ErrTemporaryLegacyDrainingCleanupConflict
	}
	first, ok := opened.FirstSlot()
	if !ok || first < opened.Segment.Start || opened.Segment.Start < target.RetiredBoundary || target.RetiredBoundary <= target.ProgressNextSlot {
		return TemporaryLegacyDrainingProgressFact{}, TemporaryLegacyDrainingTargetPlan{}, ErrTemporaryLegacyDrainingCleanupConflict
	}
	identity := execution.ProgressIdentity{QueryGroup: target.QueryGroup}
	fact, err := cleanup.progress.LoadTemporaryLegacyDrainingProgress(ctx, identity)
	if err != nil {
		return TemporaryLegacyDrainingProgressFact{}, TemporaryLegacyDrainingTargetPlan{}, err
	}
	if fact.RedisKey == "" || len(fact.Raw) == 0 || fact.Load.Validate(identity) != nil ||
		fact.Load.Status != execution.ProgressFound || fact.Load.Progress == nil ||
		fact.Load.Progress.NextSlot != target.ProgressNextSlot || fact.Load.Progress.UnfinishedSlot != nil {
		return TemporaryLegacyDrainingProgressFact{}, TemporaryLegacyDrainingTargetPlan{}, ErrTemporaryLegacyDrainingCleanupConflict
	}
	completed := fact.Load.Progress.LastFullSlot
	if fact.Load.Progress.CurrentOrRecentGap != nil && fact.Load.Progress.CurrentOrRecentGap.LastSlot > completed {
		completed = fact.Load.Progress.CurrentOrRecentGap.LastSlot
	}
	if completed <= 0 || completed >= target.ProgressNextSlot {
		return TemporaryLegacyDrainingProgressFact{}, TemporaryLegacyDrainingTargetPlan{}, ErrTemporaryLegacyDrainingCleanupConflict
	}
	return fact, TemporaryLegacyDrainingTargetPlan{
		QueryGroup: target.QueryGroup, RetiredBoundary: target.RetiredBoundary,
		ProgressNextSlot: target.ProgressNextSlot, TimelineDigest: digestBytes(timelineRaw),
		ProgressDigest: digestBytes(fact.Raw), CompletedWatermark: completed,
	}, nil
}

func (repository *RedisCatalogRepository) readOptionalRaw(ctx context.Context, key string) ([]byte, bool, error) {
	payload, err := repository.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, activationDependencyIO(err)
	}
	return payload, false, nil
}

const temporaryLegacyDrainingCleanupScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('GET', KEYS[2]) ~= ARGV[2] then return 0 end
if redis.call('GET', KEYS[3]) ~= ARGV[3] then return 0 end
if redis.call('GET', KEYS[4]) ~= ARGV[4] then return 0 end
if redis.call('GET', KEYS[5]) ~= ARGV[5] then return 0 end
if redis.call('GET', KEYS[6]) ~= ARGV[6] then return 0 end
if redis.call('GET', KEYS[7]) ~= ARGV[7] then return 0 end
local next_active = redis.call('GET', KEYS[8])
if ARGV[8] == '1' then
  if next_active then return 0 end
elseif not next_active or next_active ~= ARGV[9] then
  return 0
end
local update_count = tonumber(ARGV[10])
local progress_count = tonumber(ARGV[11])
for index = 1, update_count do
  local key_index = 8 + index
  local expected_index = 14 + (2 * index)
  local current = redis.call('GET', KEYS[key_index])
  local expected = ARGV[expected_index]
  if expected == '' then
    if current then return 0 end
  elseif not current or current ~= expected then
    return 0
  end
end
for index = 1, progress_count do
  local key_index = 8 + update_count + index
  local expected_index = 15 + (2 * update_count) + index
  if redis.call('GET', KEYS[key_index]) ~= ARGV[expected_index] then return 0 end
end
redis.call('PSETEX', KEYS[5], ARGV[12], ARGV[5])
redis.call('PSETEX', KEYS[6], ARGV[12], ARGV[6])
redis.call('PSETEX', KEYS[7], ARGV[12], ARGV[7])
redis.call('PSETEX', KEYS[8], ARGV[12], ARGV[13])
redis.call('SET', KEYS[1], ARGV[14])
redis.call('SET', KEYS[2], ARGV[15])
for index = 1, update_count do
  local key_index = 8 + index
  local next_index = 15 + (2 * index)
  redis.call('SET', KEYS[key_index], ARGV[next_index])
end
return 1
`

func (cleanup *TemporaryLegacyDrainingCleanup) persist(
	ctx context.Context,
	prepared temporaryLegacyDrainingPrepared,
) (int, error) {
	repository := cleanup.repository
	keys := []string{
		repository.activationHeaderKey(), repository.activationKey(),
		repository.activeQGSetKey(prepared.plan.CurrentActiveQGSet.Digest), repository.latestPublicationKey(),
		repository.snapshotKey(prepared.plan.Candidate.SnapshotRevision),
		repository.epochForRevisionKey(prepared.plan.Candidate.SnapshotRevision),
		repository.publicationKey(prepared.plan.Candidate.PublicationEpoch),
		repository.activeQGSetKey(prepared.plan.NextActiveQGSet.Digest),
	}
	missing := "0"
	if prepared.nextActiveMissing {
		missing = "1"
	}
	args := []interface{}{
		prepared.expectedHeader, prepared.expectedActivationRaw, prepared.expectedCurrentActiveRaw,
		prepared.expectedLatest, prepared.expectedCandidateSnapshot, prepared.expectedCandidateEpoch,
		prepared.expectedCandidateOccurrence, missing, prepared.nextActiveExisting,
		len(prepared.updates), len(prepared.progressFacts), repository.ttl.Milliseconds(), prepared.nextActivePayload,
		prepared.nextHeader, prepared.nextActivationRaw,
	}
	sort.Slice(prepared.updates, func(i, j int) bool { return prepared.updates[i].next.QueryGroup < prepared.updates[j].next.QueryGroup })
	writeKeys := make(map[string]struct{}, len(prepared.updates)+8)
	for _, key := range keys {
		writeKeys[key] = struct{}{}
	}
	for _, update := range prepared.updates {
		payload, err := json.Marshal(update.next)
		if err != nil {
			return 0, err
		}
		key := repository.scheduleTimelineKey(update.next.QueryGroup)
		keys = append(keys, key)
		writeKeys[key] = struct{}{}
		args = append(args, update.expected, payload)
	}
	seenProgress := make(map[string]struct{}, len(prepared.progressFacts))
	for _, fact := range prepared.progressFacts {
		if _, overlap := writeKeys[fact.RedisKey]; overlap {
			return 0, errors.New("alarmd controlplane: temporary cleanup Progress key overlaps control writes")
		}
		if _, duplicate := seenProgress[fact.RedisKey]; duplicate {
			return 0, errors.New("alarmd controlplane: temporary cleanup Progress key is duplicated")
		}
		seenProgress[fact.RedisKey] = struct{}{}
		keys = append(keys, fact.RedisKey)
		args = append(args, fact.Raw)
	}
	changed, err := repository.client.Eval(ctx, temporaryLegacyDrainingCleanupScript, keys, args...).Int()
	if err != nil {
		return 0, &temporaryLegacyDrainingCleanupEvalError{err: err}
	}
	return changed, nil
}

func (cleanup *TemporaryLegacyDrainingCleanup) readBackStatus(
	ctx context.Context,
	prepared temporaryLegacyDrainingPrepared,
) TemporaryLegacyDrainingCleanupApplyStatus {
	repository := cleanup.repository
	header, headerErr := repository.client.Get(ctx, repository.activationHeaderKey()).Result()
	activation, activationErr := repository.client.Get(ctx, repository.activationKey()).Bytes()
	if headerErr == nil && activationErr == nil && header == prepared.nextHeader && string(activation) == string(prepared.nextActivationRaw) {
		active, err := repository.client.Get(ctx, repository.activeQGSetKey(prepared.plan.NextActiveQGSet.Digest)).Bytes()
		if err != nil || string(active) != string(prepared.nextActivePayload) {
			return TemporaryLegacyDrainingCleanupUnknownState
		}
		for _, update := range prepared.updates {
			payload, err := json.Marshal(update.next)
			if err != nil {
				return TemporaryLegacyDrainingCleanupUnknownState
			}
			stored, err := repository.client.Get(ctx, repository.scheduleTimelineKey(update.next.QueryGroup)).Bytes()
			if err != nil || string(stored) != string(payload) {
				return TemporaryLegacyDrainingCleanupUnknownState
			}
		}
		for _, fact := range prepared.progressFacts {
			stored, err := repository.client.Get(ctx, fact.RedisKey).Bytes()
			if err != nil || string(stored) != string(fact.Raw) {
				return TemporaryLegacyDrainingCleanupUnknownState
			}
		}
		return TemporaryLegacyDrainingCleanupUnknownApplied
	}
	if headerErr == nil && activationErr == nil && header == prepared.expectedHeader && string(activation) == string(prepared.expectedActivationRaw) {
		active, missing, err := repository.readOptionalRaw(ctx, repository.activeQGSetKey(prepared.plan.NextActiveQGSet.Digest))
		if err != nil || missing != prepared.nextActiveMissing || (!missing && string(active) != string(prepared.nextActiveExisting)) {
			return TemporaryLegacyDrainingCleanupUnknownState
		}
		for _, update := range prepared.updates {
			stored, err := repository.client.Get(ctx, repository.scheduleTimelineKey(update.next.QueryGroup)).Bytes()
			if err != nil || string(stored) != string(update.expected) {
				return TemporaryLegacyDrainingCleanupUnknownState
			}
		}
		for _, fact := range prepared.progressFacts {
			stored, err := repository.client.Get(ctx, fact.RedisKey).Bytes()
			if err != nil || string(stored) != string(fact.Raw) {
				return TemporaryLegacyDrainingCleanupUnknownState
			}
		}
		return TemporaryLegacyDrainingCleanupUnknownZeroWrite
	}
	return TemporaryLegacyDrainingCleanupUnknownState
}

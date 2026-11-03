// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// MaxPlanCarries bounds the Plans one PlanCarries reading names; Total counts
// all of them.
const MaxPlanCarries = 50

// PlanCarriesReading is, for one Query Group, where each of its Plans' Runtime
// State comes from: the state generation it runs under, since when, at which
// strategy revision, and - when the activation carried the history of the
// generation it moved from (StateCarry) - that generation, the strategy
// revision it ran, and the Levels carried. A point of a series whose source
// time is before ActivatedAt was decided under CarriedFrom, which after a
// detection edit is the strategy's previous threshold or algorithm.
type PlanCarriesReading struct {
	QueryGroup execution.QueryGroupIdentity `json:"query_group"`
	Plans      []PlanCarryFact              `json:"plans"`
	Total      int                          `json:"total"`
}

// PlanCarryFact is one Plan's line of a PlanCarriesReading.
type PlanCarryFact struct {
	Plan  execution.PlanIdentity `json:"plan"`
	Shard *execution.ShardRef    `json:"shard,omitempty"`
	// StateGeneration is the generation the Plan runs under, and
	// StrategyRevision the strategy's snapshot revision in the output context
	// the open Segment last names for the Plan (0 when it names none, as a
	// strategy published without strategy_revision does not).
	//
	// ActivatedAt is the start of the earliest Segment of the unbroken run of
	// Segments that ran the Plan under this generation since it last warmed
	// up (ForceWarming: it was new, its generation moved, or it came back
	// from a hold), and CarriedFrom the carry that activation recorded. An
	// edit of another Plan of the group opens a new Segment and records this
	// Plan again without a carry, since its generation did not move, and
	// neither changes the Plan's own history; a return from a hold keeps the
	// generation, carries nothing, and is the activation. ActivationFound is
	// whether that activation is in the timeline; when it is not (the walk
	// reaches the oldest Segment held, which pruning may have cut),
	// ActivatedAt is the earliest start still held and the activation may be
	// older.
	StateGeneration  execution.StateGeneration `json:"state_generation"`
	ActivatedAt      execution.EvaluationTime  `json:"activated_at"`
	ActivationFound  bool                      `json:"activation_found"`
	StrategyRevision int64                     `json:"strategy_revision,omitempty"`
	CarriedFrom      *PlanCarrySource          `json:"carried_from,omitempty"`
}

// PlanCarrySource is the generation an activation carried history from. Found
// is whether a Segment the timeline still holds ran it, which is where
// StrategyRevision is read from; a pruned one leaves the revision unknown.
type PlanCarrySource struct {
	StateGeneration  execution.StateGeneration `json:"state_generation"`
	Levels           []uint32                  `json:"levels"`
	Found            bool                      `json:"found"`
	StrategyRevision int64                     `json:"strategy_revision,omitempty"`
}

// PlanCarries reads one Query Group's timeline and the output contexts of the
// Segments it needs: the open one, and for a carried Plan the latest Segment
// before its activation that ran the generation it carried from. Bounded by limit (at most
// MaxPlanCarries) Plans, in Plan order.
func (repository *RedisCatalogRepository) PlanCarries(ctx context.Context, queryGroup execution.QueryGroupIdentity, limit int) (PlanCarriesReading, error) {
	if limit <= 0 || limit > MaxPlanCarries {
		limit = MaxPlanCarries
	}
	timeline, err := repository.loadScheduleTimelineHinted(ctx, queryGroup)
	if err != nil {
		return PlanCarriesReading{}, err
	}
	open := -1
	for index := len(timeline.Segments) - 1; index >= 0; index-- {
		if timeline.Segments[index].Schedule.Segment.End == nil {
			open = index
			break
		}
	}
	if open < 0 {
		return PlanCarriesReading{}, ErrScheduleUnavailable
	}
	// The strategy revision is output context, not execution content (the
	// object names the strategy's source identity only): read from the
	// context the Segment last names for the Plan.
	revisions := map[execution.OutputContextDigest]int64{}
	revisionOf := func(segment persistedScheduleSegment, plan execution.PlanIdentity) (int64, error) {
		refs := segment.Schedule.Segment.OutputContextRefs
		if revised := segment.Schedule.Segment.OutputContextRevisions; len(revised) > 0 {
			refs = revised[len(revised)-1].Refs
		}
		for _, ref := range refs {
			if ref.Plan != plan || ref.Digest == "" {
				continue
			}
			revision, read := revisions[ref.Digest]
			if !read {
				output, err := repository.LoadOutputContext(ctx, ref.Digest)
				if err != nil {
					return 0, err
				}
				revision = output.StrategyRef.SnapshotRevision
				revisions[ref.Digest] = revision
			}
			return revision, nil
		}
		return 0, nil
	}
	segment := timeline.Segments[open]
	records := append([]PlanActivationRecord(nil), segment.Plans...)
	sort.Slice(records, func(i, j int) bool { return lessPlanIdentity(records[i].Fact.Plan, records[j].Fact.Plan) })
	reading := PlanCarriesReading{QueryGroup: queryGroup, Plans: []PlanCarryFact{}, Total: len(records)}
	for _, record := range records {
		if len(reading.Plans) >= limit {
			break
		}
		key := record.Fact.Key()
		selected := record.Fact.Selected
		fact := PlanCarryFact{Plan: record.Fact.Plan, Shard: record.Fact.Shard, StateGeneration: selected.StateGeneration}
		if fact.StrategyRevision, err = revisionOf(segment, record.Fact.Plan); err != nil {
			return PlanCarriesReading{}, err
		}
		// Walk back to the record that activated the Plan: the one that
		// forced it to warm up, which a new Plan, a moved generation and a
		// return from a hold all are, and nothing else is. A return from a
		// hold keeps the generation and carries nothing across the hole, so
		// the generation alone would walk past it. A Segment that does not run
		// the Plan under this generation also ends the walk.
		activated, activation := open, record
		for !activation.Fact.Selected.ForceWarming {
			if activated == 0 {
				break
			}
			earlier, ran := planRecordIn(timeline.Segments[activated-1], key)
			if !ran || earlier.Fact.Selected.StateGeneration != selected.StateGeneration {
				break
			}
			activated, activation = activated-1, earlier
		}
		fact.ActivationFound = activation.Fact.Selected.ForceWarming || activated > 0
		fact.ActivatedAt = timeline.Segments[activated].Schedule.Segment.Start
		if carry := activation.Fact.Selected.Carry; carry != nil {
			source := &PlanCarrySource{StateGeneration: carry.From, Levels: append([]uint32{}, carry.Levels...)}
			for index := activated - 1; index >= 0 && !source.Found; index-- {
				for _, earlier := range timeline.Segments[index].Plans {
					if earlier.Fact.Key() == key && earlier.Fact.Selected.StateGeneration == carry.From {
						source.Found = true
						if source.StrategyRevision, err = revisionOf(timeline.Segments[index], record.Fact.Plan); err != nil {
							return PlanCarriesReading{}, err
						}
						break
					}
				}
			}
			fact.CarriedFrom = source
		}
		reading.Plans = append(reading.Plans, fact)
	}
	return reading, nil
}

// planRecordIn is the record a Segment holds for the Plan key, if any.
func planRecordIn(segment persistedScheduleSegment, key execution.PlanKey) (PlanActivationRecord, bool) {
	for _, record := range segment.Plans {
		if record.Fact.Key() == key {
			return record, true
		}
	}
	return PlanActivationRecord{}, false
}

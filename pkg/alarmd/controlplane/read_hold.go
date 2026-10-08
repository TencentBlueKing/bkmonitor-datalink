package controlplane

import (
	"context"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ReadHoldLinkLifetime is how long a moved Plan's link to the Query Group it
// left is carried and read: the read hold record's own lifetime
// (readhold.RecordTTL). Past it the old group's record is gone and every
// deadline the link protects has passed long since, so the link carries
// nothing.
const ReadHoldLinkLifetime = 7 * 24 * time.Hour

// readHoldOrigin is a moved Plan's link as the cutover finds it on the
// Segment it closes, before it knows where the Plan goes: the state
// generation it leaves with decides, against where it goes, whether it is
// linked at all.
type readHoldOrigin struct {
	ref        ReadHoldPredecessorRef
	generation execution.StateGeneration
}

// readHoldLinker writes one cutover's links. A Plan that moves keeps the
// state it had when its state generation does not change -- a time_delay
// edit, and also a change of method, filter, metric or table -- so the two
// groups write one state and the new one's first Slots must not read ahead
// of the old one's last (h design section 5): it is linked. A Plan whose
// generation changed starts a state of its own and is not.
type readHoldLinker struct {
	origins       map[execution.PlanKey]readHoldOrigin
	carried       map[execution.PlanKey]PlanActivationRecord
	expiredBefore execution.EvaluationTime
	facts         *cutoverFacts
}

// carry writes the links of records, which group now runs. count says the
// decisions are counted: the same Plan is carried twice, once on its
// timeline and once in the activation, and decided the same way both times.
func (linker *readHoldLinker) carry(records []PlanActivationRecord, group execution.QueryGroupIdentity, count bool) {
	decided := func(decision string) {
		if count && linker.facts != nil {
			linker.facts.readHoldLinks[decision]++
		}
	}
	for i := range records {
		key := records[i].Fact.Key()
		if origin, moved := linker.origins[key]; moved && origin.ref.QueryGroup != group {
			generation := records[i].Fact.Selected.StateGeneration
			if origin.generation != "" && generation != "" && origin.generation != generation {
				decided("generation_changed")
				continue
			}
			ref := origin.ref
			if origin.generation == "" || generation == "" {
				decided("linked_generation_unknown")
			} else {
				decided("linked")
			}
			records[i].PreviousReadHold = &ref
			continue
		}
		old := linker.carried[key].PreviousReadHold
		switch {
		case old == nil:
		case old.QueryGroup == group:
			// A Plan back in the group it left before the other group ran
			// a Slot of it: its Slots are this group's own, in order.
			decided("dropped_self")
		case old.ClosedAt < linker.expiredBefore:
			decided("dropped_expired")
		default:
			copy := *old
			records[i].PreviousReadHold = &copy
		}
	}
}

// ReadHoldPredecessor is one Query Group this Segment's Plans moved from, at
// one boundary, with what each moved Plan's link says. The metadata does
// not participate in the frozen execution contract or QG ID.
type ReadHoldPredecessor struct {
	QueryGroup execution.QueryGroupIdentity
	ClosedAt   execution.EvaluationTime
	Plans      []ReadHoldLinkPlan
}

// ReadHoldLinkPlan is one moved Plan's link facts.
type ReadHoldLinkPlan struct {
	Key                    execution.PlanKey
	PreviousSlot           execution.EvaluationTime
	CompletionOffsetMillis int64
}

// ReadHoldPredecessors reads only this Segment's retained Plan links. A link
// that names this group, or that no cutover could have written, is skipped
// and counted in skipped by why -- it can only ever have been written by a
// fault, and refusing the whole group for it stopped the group for good.
func (repository *RedisCatalogRepository) ReadHoldPredecessors(ctx context.Context, schedule execution.FrozenQueryGroupSchedule) (links []ReadHoldPredecessor, skipped map[string]int, err error) {
	timeline, err := repository.loadScheduleTimelineHinted(ctx, schedule.Segment.QueryGroup)
	if err != nil {
		return nil, nil, err
	}
	for _, segment := range timeline.Segments {
		if segment.Schedule.Segment.Start != schedule.Segment.Start {
			continue
		}
		type origin struct {
			group    execution.QueryGroupIdentity
			closedAt execution.EvaluationTime
		}
		seen := map[origin]int{}
		for _, plan := range segment.Plans {
			ref := plan.PreviousReadHold
			if ref == nil {
				continue
			}
			reason := ""
			switch {
			case ref.QueryGroup == schedule.Segment.QueryGroup:
				reason = "self_link"
			case ref.QueryGroup == "" || ref.ClosedAt <= 0 || ref.ClosedAt > schedule.Segment.Start || ref.PreviousSlot >= ref.ClosedAt ||
				ref.PreviousSlot < 0 || ref.CompletionOffsetMillis < 0:
				reason = "invalid_link"
			}
			if reason != "" {
				if skipped == nil {
					skipped = map[string]int{}
				}
				skipped[reason]++
				continue
			}
			key := origin{group: ref.QueryGroup, closedAt: ref.ClosedAt}
			index, ok := seen[key]
			if !ok {
				index = len(links)
				seen[key] = index
				links = append(links, ReadHoldPredecessor{QueryGroup: ref.QueryGroup, ClosedAt: ref.ClosedAt})
			}
			links[index].Plans = append(links[index].Plans, ReadHoldLinkPlan{Key: plan.Fact.Key(), PreviousSlot: ref.PreviousSlot,
				CompletionOffsetMillis: ref.CompletionOffsetMillis})
		}
		sort.Slice(links, func(i, j int) bool {
			if links[i].QueryGroup != links[j].QueryGroup {
				return links[i].QueryGroup < links[j].QueryGroup
			}
			return links[i].ClosedAt < links[j].ClosedAt
		})
		return links, skipped, nil
	}
	return nil, nil, ErrScheduleUnavailable
}

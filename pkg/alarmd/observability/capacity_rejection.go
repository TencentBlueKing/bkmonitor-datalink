package observability

// CapacityRejectionFacts is a log-only snapshot of one failed shared
// reservation. Values use CapacityBudget units; nil OwnUsed means unknown,
// not zero. No query, key, URL, or raw error is accepted here.
type CapacityRejectionFacts struct {
	Phase                        string
	OwnUsed                      *uint64
	SharedUsed, Requested, Limit uint64
	// OthersUsed is what others held of the pool when it refused a Query
	// Group's Slot within its share (RESOURCE_POOL_HELD_BY_OTHERS): the pool's
	// reserved bytes less the Slot's own. Unset on every other refusal.
	OthersUsed *uint64
	// Usage is what this execution had taken of every budget at the moment one
	// of them refused, each against the limit it was measured with.
	//
	// Without it a rejection names only the budget that was reached, and
	// "the count refused while the byte budget was ninety percent free" cannot
	// be said - which is the one sentence that distinguishes a process at its
	// memory limit from a process stopped by a number standing in for memory.
	// Reading it from the ceilings in /metrics does not work either: those give
	// the limits, never what this Slot had of them.
	Usage []CapacityBudgetUsage
}

// CapacityBudgetUsage is one budget's own usage at the moment of a rejection,
// beside the limit it was compared against.
type CapacityBudgetUsage struct {
	Budget  CapacityBudget
	OwnUsed uint64
	Limit   uint64
}

func normalizeCapacityRejection(observation Observation) *CapacityRejectionFacts {
	if observation.Component != ComponentResource || observation.Stage != StageResourceHard ||
		observation.Err == nil || observation.CapacityBudget == "" || observation.CapacityRejection == nil {
		return nil
	}
	f := *observation.CapacityRejection
	switch f.Phase {
	case "normal_input", "normal_gap", "normal_output", "normal_no_data", "slot_output", "query_free", "snapshot_prepare":
	default:
		f.Phase = "other"
	}
	if f.OwnUsed != nil {
		own := *f.OwnUsed
		f.OwnUsed = &own
	}
	// Copied, and only for budgets this build names: the facts arrive from the
	// caller's own struct, and a row is a log line rather than a place to
	// forward whatever a future budget calls itself.
	if len(f.Usage) != 0 {
		usage := make([]CapacityBudgetUsage, 0, len(f.Usage))
		for _, entry := range f.Usage {
			if !knownCapacityBudget(entry.Budget) {
				continue
			}
			usage = append(usage, entry)
		}
		f.Usage = usage
	}
	return &f
}

func knownCapacityBudget(budget CapacityBudget) bool {
	switch budget {
	case CapacityBudgetSeries, CapacityBudgetRetainedBytes, CapacityBudgetStateMutations,
		CapacityBudgetEvents, CapacityBudgetGapMutations:
		return true
	}
	return false
}

// SlotBudgetUsageFacts is what one Slot took of each budget, reported on its
// completion row whatever the outcome.
//
// It is on the completion row rather than only on rejections because a number
// that exists only when something failed has no distribution behind it: there
// is nothing to take a median of, so "this budget is the one under pressure"
// cannot be told from "this budget is nowhere near its limit". Both halves are
// carried - what was used and what it was measured against - because a usage
// without its limit is not a reading, and the limits in /metrics are the
// process ceilings rather than what this Slot was admitted against.
type SlotBudgetUsageFacts struct {
	StateMutations uint64 `json:"state_mutations"`
	GapMutations   uint64 `json:"gap_mutations"`
	Events         uint64 `json:"events"`
	// EventsWithoutMessage is the events decided and not kept because their
	// protocol has no message for them; Events plus this is what Events
	// counted before a Python-compatible RECOVERY stopped being held.
	EventsWithoutMessage uint64 `json:"events_without_message"`
	RetainedBytes        uint64 `json:"retained_bytes"`
	Series               uint64 `json:"series"`
	// DependencySeriesDiscarded is the series a dependency query answered
	// that no PRIMARY input of the same Plan named, let go at the completion
	// (input_series_discarded_total{reason="dependency_without_primary"}).
	DependencySeriesDiscarded uint64 `json:"dependency_series_discarded"`
	// InputSlices is the slices the Slot's input was read in, one for a Slot
	// read whole, and InputSlicesDiscarded the ones let go and split while
	// they streamed. FirstSeenUnseenSlices and FirstSeenUnseenSeries are the
	// slices, and their series, a Plan judged first-seen wrote before a later
	// slice found it had run: they were written without warming up.
	InputSlices           uint64 `json:"input_slices"`
	InputSlicesDiscarded  uint64 `json:"input_slices_discarded"`
	FirstSeenUnseenSlices uint64 `json:"first_seen_unseen_slices"`
	FirstSeenUnseenSeries uint64 `json:"first_seen_unseen_series"`
	// InputSlicesReadAhead is the slices read while the slice before
	// completed and handed to the Slot; the other three the reads ahead let
	// go, by why: the next slice was another, the read yielded to the Slot's
	// own reservation, or it failed. Each let go was read again after.
	// SliceReadAheadOutcomes names the four, in this order.
	InputSlicesReadAhead          uint64 `json:"input_slices_read_ahead,omitempty"`
	InputSlicesReadAheadOtherLeaf uint64 `json:"input_slices_read_ahead_other_leaf,omitempty"`
	InputSlicesReadAheadYielded   uint64 `json:"input_slices_read_ahead_yielded,omitempty"`
	InputSlicesReadAheadFailed    uint64 `json:"input_slices_read_ahead_failed,omitempty"`
	// InputSliceTimeouts is the slices that ran out of time and were split
	// and read again, the Slot completing: at most one a Slot.
	InputSliceTimeouts uint64 `json:"input_slice_timeouts,omitempty"`
	// InputSliceShape* is what the Slot did with its Query Group's shape,
	// one each at most (InputSliceShapeOutcomes).
	InputSliceShapeStarted              uint64 `json:"input_slice_shape_started,omitempty"`
	InputSliceShapeKept                 uint64 `json:"input_slice_shape_kept,omitempty"`
	InputSliceShapeMerged               uint64 `json:"input_slice_shape_merged,omitempty"`
	InputSliceShapeVoidedRevision       uint64 `json:"input_slice_shape_voided_revision,omitempty"`
	InputSliceShapeVoidedDimensions     uint64 `json:"input_slice_shape_voided_dimensions,omitempty"`
	InputSliceShapeVoidedMaxLeaves      uint64 `json:"input_slice_shape_voided_max_leaves,omitempty"`
	InputSliceShapeRefusedNotALeaf      uint64 `json:"input_slice_shape_refused_not_a_leaf,omitempty"`
	InputSliceShapeRefusedNotAPartition uint64 `json:"input_slice_shape_refused_not_a_partition,omitempty"`
	InputSliceShapeRefusedMaxLeaves     uint64 `json:"input_slice_shape_refused_max_leaves,omitempty"`
	InputSliceShapeEvictedIdle          uint64 `json:"input_slice_shape_evicted_idle,omitempty"`
	// InputSliceCutOn is where the dimension the Slot's starting slices
	// were cut on came from (InputSliceCutOnSources), empty for a Slot not
	// started in slices counted: read whole, or in its Query Group's shape.
	InputSliceCutOn string `json:"input_slice_cut_on,omitempty"`
	// InputSliceDimension is the dimension the Slot's slices were cut on at
	// their root, however they started - cut counted, or in the Query
	// Group's shape - empty for a Slot read whole.
	InputSliceDimension string `json:"input_slice_dimension,omitempty"`
	// InputSlicesDeferred is the slices read last for the provider having
	// timed them out the Slot before, completing degraded: judged against
	// the slices read before them.
	InputSlicesDeferred uint64 `json:"input_slices_deferred,omitempty"`
	// InputSliceStartDiscarded is 1 when a slice the Slot started in from
	// its memory - the cut a read before chose, or its shape - was let go
	// for its size: the cut did not fit the input.
	InputSliceStartDiscarded uint64 `json:"input_slice_start_discarded,omitempty"`
	// NoDataStagesAhead* is how the Slot's no-data first writes staged as
	// their windows landed ended, a Plan each (NoDataStageAheadOutcomes);
	// NoDataStageAheadDiffers* the differs among them by why
	// (nodata.StagedDiffersReasons); NoDataGroupsStagedAhead the groups
	// they staged.
	NoDataStagesAheadCarried             uint64 `json:"no_data_stages_ahead_carried,omitempty"`
	NoDataStagesAheadDiffers             uint64 `json:"no_data_stages_ahead_differs,omitempty"`
	NoDataStagesAheadDiscarded           uint64 `json:"no_data_stages_ahead_discarded,omitempty"`
	NoDataStagesAheadStopped             uint64 `json:"no_data_stages_ahead_stopped,omitempty"`
	NoDataStageAheadDiffersKeptOtherwise uint64 `json:"no_data_stage_ahead_differs_kept_otherwise,omitempty"`
	NoDataStageAheadDiffersCountMismatch uint64 `json:"no_data_stage_ahead_differs_count_mismatch,omitempty"`
	NoDataStageAheadDiffersPresentAsOf   uint64 `json:"no_data_stage_ahead_differs_present_as_of,omitempty"`
	NoDataGroupsStagedAhead              uint64 `json:"no_data_groups_staged_ahead,omitempty"`

	// RetainedBytes split by what the memory was held for, summing to it.
	//
	// These stay on the row where the limits do not, because the limits are
	// process constants a reader can look up once while these are this Slot's
	// own and vary round to round. Reported at every value including zero: a
	// phase that is usually nothing and occasionally the whole budget is the
	// one worth finding, and omitting its zeros would leave it with no
	// denominator to be occasional against.
	RetainedInputBytes  uint64 `json:"retained_input_bytes"`
	RetainedGapBytes    uint64 `json:"retained_gap_bytes"`
	RetainedOutputBytes uint64 `json:"retained_output_bytes"`
	RetainedStateBytes  uint64 `json:"retained_state_bytes"`

	// The limits stay off the row and are carried for readers that hold the
	// facts rather than the log line.
	//
	// They are process constants: every Slot on a replica is measured against
	// the same five, they are already published once per process, and repeating
	// them on every completion row doubles the row for nothing. At one row per
	// object per Slot that is most of a gigabyte a day to restate numbers that
	// did not change.
	StateMutationsLimit uint64 `json:"-"`
	GapMutationsLimit   uint64 `json:"-"`
	EventsLimit         uint64 `json:"-"`
	RetainedBytesLimit  uint64 `json:"-"`
	SeriesLimit         uint64 `json:"-"`
	// RetainedShareBytes is the one Query Group's share of the retained pool
	// this Slot was admitted against, from the producer that refuses by it.
	RetainedShareBytes uint64 `json:"-"`

	// Windows is how many windows the Slot was applied in, zero for one
	// applied whole; WindowCeilingWindows how many the watchdog's ceiling
	// allows it, and WindowCeilingRaisedWindows how many of those a no-data
	// round's absences added after its scan (decision-020 batch 8, 2.2 step
	// 6 b). A Slot claimed past its ceiling is read against these.
	Windows                    uint64 `json:"windows"`
	WindowCeilingWindows       uint64 `json:"window_ceiling_windows"`
	WindowCeilingRaisedWindows uint64 `json:"window_ceiling_raised_windows"`
}

// SlotTimingFacts is where one Slot's wall clock went, in milliseconds, on its
// completion row.
//
// Its own object beside the budget usage because it answers a different
// question. The usage says what the Slot consumed of what it was allowed; this
// says where its period went, and the two are read by different people at
// different moments -- one when a budget is near its limit, the other when a
// Slot did not finish in time.
//
// Slot is the whole of it and the three parts do not sum to it: the remainder
// is everything else the completion does, and it is only visible because the
// total is carried beside the parts.
//
// Input is this Slot waiting for its records and consuming them, from the
// moment the replica took the Slot and not from the first record. Not the
// query's own latency -- that runs on the view stream's side, and a Slot that
// waited its turn waited here with a backend that was never slow.
//
// Reported at every value including zero, for the same reason the retained
// phases are: a phase that is usually nothing and occasionally the whole Slot
// is the one worth finding, and its zeros are its denominator.
//
// From the completion on the phases partition the Slot, and Unattributed is
// what none of them claims (see execution.SlotTiming).
type SlotTimingFacts struct {
	Slot         uint64 `json:"slot_millis"`
	Input        uint64 `json:"input_millis"`
	Preflight    uint64 `json:"preflight_millis"`
	Evaluate     uint64 `json:"evaluate_millis"`
	Apply        uint64 `json:"apply_millis"`
	RecordIDs    uint64 `json:"record_ids_millis"`
	Cut          uint64 `json:"cut_millis"`
	Merge        uint64 `json:"merge_millis"`
	Series       uint64 `json:"series_millis"`
	NoDataRead   uint64 `json:"no_data_read_millis"`
	NoDataWrite  uint64 `json:"no_data_write_millis"`
	Unattributed uint64 `json:"unattributed_millis"`
}

// SliceReadAheadOutcomes are the words for how a slice read ahead ended, in
// the order of the SlotBudgetUsageFacts fields that count them: replayed
// (InputSlicesReadAhead), other_leaf, yielded and failed.
var SliceReadAheadOutcomes = [4]string{"replayed", "other_leaf", "yielded", "failed"}

// How a slice's read that ran out of time ended, closed: split and read
// again, the Slot's query completing; split and read again, the Slot's query
// failing after; not split - the cooldown held, its time could not be put on
// its size, the window left was too short, or the Slot had split once.
const (
	InputSliceTimeoutSplitSucceeded = "split_succeeded"
	InputSliceTimeoutSplitFailed    = "split_failed"
	InputSliceTimeoutNotSplit       = "not_split"
)

// NoDataStageAheadOutcomes are how a no-data first write staged as its
// windows landed ended, in this order: carried, the statement put the stage
// in place with it; differs, the round kept a staged group otherwise and
// wrote every group with the statement; discarded, the round wrote no
// statement and the stage was taken back; stopped, a segment could not be
// staged and the groups after it went with the statement.
var NoDataStageAheadOutcomes = [4]string{"carried", "differs", "discarded", "stopped"}

// What a Slot did with its Query Group's shape - the leaves a slice that ran
// out of time for its size was split into, which the next Slot starts in -
// closed: started, read in it; kept, a slice split for its time kept in it;
// merged, its last kept split taken back after quick rounds; dropped, by
// why: voided, the query revision or the dimensions changed or it grew past
// a starting tree's leaves; refused, its leaves not a partition of every
// value (inputslice.FromShape: a split of no leaf, children that are no
// partition, more leaves than a tree may have); evicted_idle, the memories
// of other Query Groups not started for ten of their periods and ten
// minutes at least, shape or none, that a Slot's start dropped.
const (
	InputSliceShapeStarted              = "started"
	InputSliceShapeKept                 = "kept"
	InputSliceShapeMerged               = "merged"
	InputSliceShapeVoidedRevision       = "voided_revision"
	InputSliceShapeVoidedDimensions     = "voided_dimensions"
	InputSliceShapeVoidedMaxLeaves      = "voided_max_leaves"
	InputSliceShapeRefusedNotALeaf      = "refused_not_a_leaf"
	InputSliceShapeRefusedNotAPartition = "refused_not_a_partition"
	InputSliceShapeRefusedMaxLeaves     = "refused_max_leaves"
	InputSliceShapeEvictedIdle          = "evicted_idle"
)

// InputSliceShapeOutcomes is every outcome, in the order of the
// SlotBudgetUsageFacts fields that count them.
var InputSliceShapeOutcomes = [10]string{InputSliceShapeStarted, InputSliceShapeKept, InputSliceShapeMerged,
	InputSliceShapeVoidedRevision, InputSliceShapeVoidedDimensions, InputSliceShapeVoidedMaxLeaves,
	InputSliceShapeRefusedNotALeaf, InputSliceShapeRefusedNotAPartition, InputSliceShapeRefusedMaxLeaves,
	InputSliceShapeEvictedIdle}

// Where the dimension a Slot's starting slices were cut on came from,
// closed: root_split, the one its Query Group's last read had its root split
// on; whole_sample, the one the sample of its last read, read whole, split
// most evenly; first_dimension_fallback, the first of its dimensions, for a
// Query Group no read ever delivered a series of - its read whole the
// provider timed out - which nothing else can choose for.
const (
	InputSliceCutOnRootSplit      = "root_split"
	InputSliceCutOnWholeSample    = "whole_sample"
	InputSliceCutOnFirstDimension = "first_dimension_fallback"
)

// InputSliceCutOnSources is every source.
var InputSliceCutOnSources = [3]string{InputSliceCutOnRootSplit, InputSliceCutOnWholeSample, InputSliceCutOnFirstDimension}

// InputSliceTimeoutOutcomes is every outcome.
var InputSliceTimeoutOutcomes = []string{InputSliceTimeoutSplitSucceeded, InputSliceTimeoutSplitFailed, InputSliceTimeoutNotSplit}

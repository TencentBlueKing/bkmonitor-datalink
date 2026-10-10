package observability

// CapacityRejectionFacts is a log-only snapshot of one failed shared
// reservation. Values use CapacityBudget units; nil OwnUsed means unknown,
// not zero. No query, key, URL, or raw error is accepted here.
type CapacityRejectionFacts struct {
	Phase                        string
	OwnUsed                      *uint64
	SharedUsed, Requested, Limit uint64
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
	case "normal_input", "normal_gap", "normal_output", "slot_output", "query_free", "snapshot_prepare":
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
type SlotTimingFacts struct {
	Slot      uint64 `json:"slot_millis"`
	Input     uint64 `json:"input_millis"`
	Preflight uint64 `json:"preflight_millis"`
	Evaluate  uint64 `json:"evaluate_millis"`
}

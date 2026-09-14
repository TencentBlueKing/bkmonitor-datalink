package observability

// StateWriteReuseClass says how much of what a State write carries was already
// stored for the same key.
type StateWriteReuseClass string

const (
	// StateWriteReuseUnobserved is a key with nothing stored to compare with.
	StateWriteReuseUnobserved StateWriteReuseClass = "unobserved"
	// StateWriteReuseIdentical is the whole blob reproduced.
	StateWriteReuseIdentical StateWriteReuseClass = "identical"
	// StateWriteReuseDecisionStable is the Level state and series guard
	// unchanged while the rest of the blob moved.
	StateWriteReuseDecisionStable StateWriteReuseClass = "decision_stable"
	// StateWriteReuseChanged is a moved Level state or series guard.
	StateWriteReuseChanged StateWriteReuseClass = "changed"
	// StateWriteReuseClassOther keeps an unrecognised class from opening the
	// label set; it should never be produced.
	StateWriteReuseClassOther StateWriteReuseClass = "other"
)

// StateWriteReuseStored is the stored state a comparison was made against. It
// exists because "steady" is not one population: a series that keeps recovering
// and a series that never leaves history warming are both steady and both pay a
// write every round, but they reach that write through different branches and
// need not be reusable to the same degree. Counted together they would average
// into a single rate that describes neither, and a change sized against that
// average would be sized against nothing real.
type StateWriteReuseStored string

const (
	StateWriteReuseStoredReady   StateWriteReuseStored = "found_ready"
	StateWriteReuseStoredWarming StateWriteReuseStored = "found_warming"
	StateWriteReuseStoredGapped  StateWriteReuseStored = "found_gapped"
	StateWriteReuseStoredMissing StateWriteReuseStored = "missing_warming"
	StateWriteReuseStoredOther   StateWriteReuseStored = "other"
)

// NormalizeStateWriteReuseClass and NormalizeStateWriteReuseStored bound the
// label set. An unrecognised value becomes "other" rather than travelling
// through: an open label set on a per-series counter is how a metric turns
// into a cardinality incident.
func NormalizeStateWriteReuseClass(class StateWriteReuseClass) StateWriteReuseClass {
	switch class {
	case StateWriteReuseUnobserved, StateWriteReuseIdentical,
		StateWriteReuseDecisionStable, StateWriteReuseChanged:
		return class
	default:
		return StateWriteReuseClassOther
	}
}

func NormalizeStateWriteReuseStored(stored StateWriteReuseStored) StateWriteReuseStored {
	switch stored {
	case StateWriteReuseStoredReady, StateWriteReuseStoredWarming,
		StateWriteReuseStoredGapped, StateWriteReuseStoredMissing:
		return stored
	default:
		return StateWriteReuseStoredOther
	}
}

// StateWriteReuseKey is one cell of the reading: how much was reusable, and
// what the stored state was when the comparison was made.
type StateWriteReuseKey struct {
	Class  StateWriteReuseClass
	Stored StateWriteReuseStored
}

// StateWriteReuseFacts counts, for one Plan's State apply, how the mutations
// admitted for writing compared against what was already stored. It measures a
// change that has not been made: nothing skips a write yet, and these counts
// are the only way to size a skip before the write path is touched.
//
// The classes are reported separately and never summed into one "hit rate".
// Identical and DecisionStable answer different questions: the first is what a
// whole-blob skip could save, the second what a skip could save once the
// history window stops travelling with the decision state. Added together they
// would report a saving no single change can deliver.
//
// Unobserved is its own class rather than being folded into Changed. A worker
// that has just started witnesses no stored digest, so every round would
// otherwise land in Changed and depress the rate for as long as the warm-up
// lasts; kept apart, the warm-up is visible instead of averaged in.
type StateWriteReuseFacts struct {
	Counts map[StateWriteReuseKey]int64 `json:"counts"`
	// ChangeReasons is only populated for comparisons that came back changed,
	// and says which field differed first. It is a separate family rather than
	// a third label on the classes because a reason is meaningless for the
	// other classes, and a label that is "none" for most of a counter's
	// population makes both harder to read.
	ChangeReasons map[StateWriteChangeKey]int64 `json:"change_reasons"`
}

// Record records one comparison, normalising every label. The class and its
// reason are recorded in one call so a caller cannot count one without the
// other and leave two families that disagree on how many comparisons happened.
func (facts *StateWriteReuseFacts) Record(
	class StateWriteReuseClass, reason StateWriteChangeReason, stored StateWriteReuseStored,
) {
	normalizedClass := NormalizeStateWriteReuseClass(class)
	normalizedStored := NormalizeStateWriteReuseStored(stored)
	if facts.Counts == nil {
		facts.Counts = map[StateWriteReuseKey]int64{}
	}
	facts.Counts[StateWriteReuseKey{Class: normalizedClass, Stored: normalizedStored}]++
	if normalizedClass != StateWriteReuseChanged {
		return
	}
	if facts.ChangeReasons == nil {
		facts.ChangeReasons = map[StateWriteChangeKey]int64{}
	}
	facts.ChangeReasons[StateWriteChangeKey{
		Reason: NormalizeStateWriteChangeReason(reason),
		Stored: normalizedStored,
	}]++
}

// Total is the population every class is counted out of. It is the positive
// control: it is non-zero on any process that admits a State write, so all
// classes zero with a zero total means the classifier never ran, while all
// classes zero with a non-zero total would be a real result. Without it an
// all-zero reading cannot tell a measured absence from a probe that is not
// wired up -- and this measurement expects a genuine zero in Identical.
func (facts StateWriteReuseFacts) Total() int64 {
	var total int64
	for _, count := range facts.Counts {
		total += count
	}
	return total
}

// Empty reports whether nothing was classified, so a caller can leave the facts
// off an observation rather than emitting zeros that read as a result.
func (facts StateWriteReuseFacts) Empty() bool { return facts.Total() == 0 }

// StateWriteChangeReason names the first field a changed comparison found
// different. The class alone cannot be acted on: "changed" covers a decision
// that truly moved, which would end the case for skipping the write, and a
// field that should never have counted as part of the decision, which would
// mean the predicate is wrong rather than the idea. Those point at opposite
// actions and are indistinguishable without the field name.
type StateWriteChangeReason string

const (
	StateWriteChangeNone          StateWriteChangeReason = "none"
	StateWriteChangeLevelCount    StateWriteChangeReason = "level_count"
	StateWriteChangeLevelMissing  StateWriteChangeReason = "level_missing"
	StateWriteChangeCompatibility StateWriteChangeReason = "level_compatibility"
	StateWriteChangeCompleteness  StateWriteChangeReason = "history_completeness"
	StateWriteChangeGapReason     StateWriteChangeReason = "gap_reason"
	StateWriteChangeWarmupRef     StateWriteChangeReason = "warmup_ref"
	StateWriteChangeProcessedTime StateWriteChangeReason = "processed_time"
	StateWriteChangeSeriesGuard   StateWriteChangeReason = "series_guard"
	StateWriteChangeReasonOther   StateWriteChangeReason = "other"
)

// AllStateWriteChangeReasons is the complete bounded set, "other" included.
func AllStateWriteChangeReasons() []StateWriteChangeReason {
	return []StateWriteChangeReason{
		StateWriteChangeNone, StateWriteChangeLevelCount, StateWriteChangeLevelMissing,
		StateWriteChangeCompatibility, StateWriteChangeCompleteness, StateWriteChangeGapReason,
		StateWriteChangeWarmupRef, StateWriteChangeProcessedTime, StateWriteChangeSeriesGuard,
		StateWriteChangeReasonOther,
	}
}

func NormalizeStateWriteChangeReason(reason StateWriteChangeReason) StateWriteChangeReason {
	for _, known := range AllStateWriteChangeReasons() {
		if known == reason && known != StateWriteChangeReasonOther {
			return reason
		}
	}
	return StateWriteChangeReasonOther
}

// StateWriteChangeKey is one cell of the first-difference reading.
type StateWriteChangeKey struct {
	Reason StateWriteChangeReason
	Stored StateWriteReuseStored
}

// AllStateWriteReuseClasses and AllStateWriteReuseStored are the complete label
// sets, "other" included. They are the one place these values are enumerated:
// the recorder publishes each combination from the first scrape and the
// cardinality bound is computed from their sizes, so a class added to one and
// forgotten in the other cannot happen.
func AllStateWriteReuseClasses() []StateWriteReuseClass {
	return []StateWriteReuseClass{
		StateWriteReuseUnobserved, StateWriteReuseIdentical,
		StateWriteReuseDecisionStable, StateWriteReuseChanged, StateWriteReuseClassOther,
	}
}

func AllStateWriteReuseStored() []StateWriteReuseStored {
	return []StateWriteReuseStored{
		StateWriteReuseStoredReady, StateWriteReuseStoredWarming, StateWriteReuseStoredGapped,
		StateWriteReuseStoredMissing, StateWriteReuseStoredOther,
	}
}

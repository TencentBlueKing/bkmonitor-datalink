package fleet

import (
	"encoding/json"
	"sort"
)

// ReadHoldFacts is a compact projection of the owner's fenced record. A
// group its replica leaves out of a whole list holds nothing: one whose hold
// has only ever been zero keeps no record. Unknown is a group whose record
// did not decode or is not read yet; and a group left out of a list cut to
// its budget (Snapshot.ReadHoldsCut), or held by no one replica, is unknown
// too (View.readHoldOf). A replica of a build before this contract leaves
// out the unknown ones as well, so while a rollout mixes builds, a group of
// an old replica that is not listed can read as holding nothing.
type ReadHoldFacts struct {
	Unknown             bool   `json:"unknown,omitempty"`
	Millis              int64  `json:"read_hold_ms"`
	ArrivalAgeMillis    int64  `json:"arrival_age_ms"`
	LimitMillis         int64  `json:"limit_ms"`
	AtLimit             bool   `json:"at_limit"`
	RaisedAfterLowering uint64 `json:"raised_after_lowering"`
	// NoWholeWindowArrival is the samples classed window read early in
	// which no series arrived whole after the first read.
	NoWholeWindowArrival uint64  `json:"no_whole_window_arrival"`
	Rung                 string  `json:"rung,omitempty"`
	Buckets              []int64 `json:"buckets,omitempty"`
	Annotation           string  `json:"annotation"`
	// HeldSince is the first Slot frozen with the group's hold in force, Unix
	// seconds; zero while it holds none, or has only chosen one.
	HeldSince int64 `json:"held_since,omitempty"`
	// DelaySeconds is the time_delay the group's query runs under, as
	// compiled, and SuggestedDelaySeconds the one at which its measured
	// arrival age needs no hold: the arrival age less the settling wait,
	// aligned up to the data step as the lookback's suggestion is. An upper
	// bound, as the arrival age only rises with the evidence until a
	// lowering, itself three matching earlier reads, sets it lower. Zero
	// while the group holds nothing, its arrival age was never measured --
	// a hold that is a predecessor's bound -- or its query is not known yet.
	DelaySeconds          int64 `json:"time_delay_seconds,omitempty"`
	SuggestedDelaySeconds int64 `json:"suggested_time_delay_seconds,omitempty"`
	// SettlingWaitSeconds is the wait alarmd adds after the time_delay before
	// a Slot is ready, which the suggestion was reckoned net of: why the
	// time_delay suggested can be less than the arrival age.
	SettlingWaitSeconds int64 `json:"settling_wait_seconds,omitempty"`
}

func withinReadHoldBudget(facts map[string]ReadHoldFacts, budget int) map[string]ReadHoldFacts {
	if budget <= 0 || len(facts) == 0 {
		return facts
	}
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	kept, spent := map[string]ReadHoldFacts{}, 2
	for _, key := range keys {
		entry, err := json.Marshal(map[string]ReadHoldFacts{key: facts[key]})
		size := len(entry) - 2
		if len(kept) > 0 {
			size++
		}
		if err != nil || spent+size > budget {
			break
		}
		spent += size
		kept[key] = facts[key]
	}
	return kept
}

// readHoldOf is a Query Group's read hold as the view knows it: the facts its
// replica published; nil -- no hold -- when that replica published its list
// whole and left the group out, as a group whose hold has only ever been
// zero keeps no record; and unknown when the list was cut, or no one replica
// is known to hold the group.
func (view *View) readHoldOf(queryGroup string) *ReadHoldFacts {
	if reading, known := view.readHolds[queryGroup]; known {
		return &reading
	}
	replica, owned := view.ownerOf[queryGroup]
	if !owned || view.readHoldAmbiguous[queryGroup] || !view.readHoldsWhole[replica] {
		return &ReadHoldFacts{Unknown: true}
	}
	return nil
}

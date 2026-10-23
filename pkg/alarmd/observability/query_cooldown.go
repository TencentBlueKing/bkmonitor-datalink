package observability

import "time"

const StageQueryCooldown = "query_cooldown"

// QueryCooldownFacts describes a policy transition or a failed real probe.
// Query Group identity belongs in Trace, never in metric labels.
type QueryCooldownFacts struct {
	Event       string    `json:"event"`
	Until       time.Time `json:"until"`
	LastQueryAt time.Time `json:"last_query_at"`
	Failures    uint32    `json:"failures"` // Saturates at 32; 32 means at least 32.
	// Timeouts is how many of the failures were the backend not answering
	// before the deadline, FirstTimeoutAt when the first was; saturates at 32.
	Timeouts       uint32    `json:"timeouts,omitempty"`
	FirstTimeoutAt time.Time `json:"first_timeout_at,omitempty"`
	// EnteredAt is when the Query Group entered the pool, across restarts and
	// owners; Source says how it is in it now: probe (this process's own
	// failed queries) or restored (read back from its record). LastExitAt and
	// LastExitReason are its last exit, and Reentries how many times it came
	// back within QueryCooldownReentryWindow of one.
	EnteredAt      time.Time `json:"entered_at,omitempty"`
	Source         string    `json:"source,omitempty"`
	LastExitAt     time.Time `json:"last_exit_at,omitempty"`
	LastExitReason string    `json:"last_exit_reason,omitempty"`
	Reentries      uint32    `json:"reentries,omitempty"`
	// LastProbe is the latest execution the pool let run, the one whose
	// answer decides whether the Query Group stays or leaves: kept across
	// the exit it caused, so a reader sees what the leaving answer had and
	// used. Nil before the pool let any run.
	LastProbe *QueryCooldownProbe `json:"last_probe,omitempty"`
	// CeilingMillis is the backend's own cut as this Query Group's failures
	// showed it: the shortest a query of it ran before the backend answered
	// unavailable, among the failures that ran longer than any of its
	// answers. Zero when none has. RunSpreadMillis is the spread of its
	// recent answered run times, the margin a probe must clear the ceiling
	// by. Probes are its latest deciding probes, oldest first, each with the
	// comparison that decided it.
	CeilingMillis   int64                `json:"ceiling_ms,omitempty"`
	RunSpreadMillis int64                `json:"run_spread_ms,omitempty"`
	Probes          []QueryCooldownProbe `json:"probes,omitempty"`
}

// QueryCooldownProbe is one execution the pool let run. Outcome is what its
// primary query proved: answered, unavailable, or unknown (nothing either
// way). BudgetMillis and ElapsedMillis are its slowest primary query's
// budget from the send and time to come back, the budget being what a
// normal round of the same Slot has; Measured is false when no primary
// query was timed.
type QueryCooldownProbe struct {
	At            time.Time `json:"at"`
	Operation     string    `json:"operation"`
	Outcome       string    `json:"outcome"`
	Measured      bool      `json:"measured"`
	BudgetMillis  int64     `json:"budget_ms,omitempty"`
	ElapsedMillis int64     `json:"elapsed_ms,omitempty"`
	// Rule is the comparison that decided an answered probe
	// (QueryCooldownExitRules), with the ceiling and spread it was held to;
	// empty for a probe that did not answer, which stays by its failure.
	Rule            string `json:"rule,omitempty"`
	CeilingMillis   int64  `json:"ceiling_ms,omitempty"`
	RunSpreadMillis int64  `json:"run_spread_ms,omitempty"`
}

// The comparisons that decide an answered probe, closed. Every one but
// near_ceiling takes the Query Group out of the pool.
//
// no_ceiling: none of its failures showed a backend cut, so an answer is
// the evidence it always was. unmeasured: the answer was not timed and
// cannot be compared. above_ceiling: it answered past the ceiling, so the
// cut it was held to is no longer there, and the ceiling is dropped.
// under_ceiling: it answered under the ceiling by more than its own runs
// spread. answered_in_row: it answered as many deciding probes in a row as
// its run times keep, near the ceiling every time; a backend that answers
// that often is answering its normal rounds. near_ceiling: it answered
// within its spread of the cut, where the next normal round is as likely to
// be cut as not; it stays, and the next probe decides.
const (
	QueryCooldownRuleNoCeiling     = "no_ceiling"
	QueryCooldownRuleUnmeasured    = "unmeasured"
	QueryCooldownRuleAboveCeiling  = "above_ceiling"
	QueryCooldownRuleUnderCeiling  = "under_ceiling"
	QueryCooldownRuleAnsweredInRow = "answered_in_row"
	QueryCooldownRuleNearCeiling   = "near_ceiling"
)

// QueryCooldownExitRules is the closed set a probe's Rule takes.
var QueryCooldownExitRules = []string{QueryCooldownRuleNoCeiling, QueryCooldownRuleUnmeasured,
	QueryCooldownRuleAboveCeiling, QueryCooldownRuleUnderCeiling, QueryCooldownRuleAnsweredInRow,
	QueryCooldownRuleNearCeiling}

// The outcomes a QueryCooldownProbe records, closed.
const (
	QueryCooldownProbeAnswered    = "answered"
	QueryCooldownProbeUnavailable = "unavailable"
	QueryCooldownProbeUnknown     = "unknown"
)

// QueryCooldownEvents is the pool's closed event vocabulary: the metric's
// label values and every word the fleet tracker acts on.
var QueryCooldownEvents = []string{"entered", "reentered", "extended", "restored",
	"recovered", "query_revision_changed"}

func normalizeQueryCooldownFacts(facts *QueryCooldownFacts) *QueryCooldownFacts {
	if facts == nil {
		return nil
	}
	copy := *facts
	switch copy.Event {
	case "entered", "reentered", "extended", "restored", "recovered", "query_revision_changed":
	default:
		copy.Event = "other"
	}
	return &copy
}

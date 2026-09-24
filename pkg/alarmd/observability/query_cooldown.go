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
}

// QueryCooldownEvents is the pool's closed event vocabulary: the metric's
// label values and every word the fleet tracker acts on.
var QueryCooldownEvents = []string{"entered", "reentered", "extended", "restored",
	"recovered", "query_revision_changed", "disabled"}

func normalizeQueryCooldownFacts(facts *QueryCooldownFacts) *QueryCooldownFacts {
	if facts == nil {
		return nil
	}
	copy := *facts
	switch copy.Event {
	case "entered", "reentered", "extended", "restored", "recovered", "query_revision_changed", "disabled":
	default:
		copy.Event = "other"
	}
	return &copy
}

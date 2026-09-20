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
}

func normalizeQueryCooldownFacts(facts *QueryCooldownFacts) *QueryCooldownFacts {
	if facts == nil {
		return nil
	}
	copy := *facts
	switch copy.Event {
	case "entered", "extended", "recovered", "config_changed", "disabled":
	default:
		copy.Event = "other"
	}
	return &copy
}

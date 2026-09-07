package observability

// CapacityRejectionFacts is a log-only snapshot of one failed shared
// reservation. Values use CapacityBudget units; nil OwnUsed means unknown,
// not zero. No query, key, URL, or raw error is accepted here.
type CapacityRejectionFacts struct {
	Phase                        string
	OwnUsed                      *uint64
	SharedUsed, Requested, Limit uint64
}

func normalizeCapacityRejection(observation Observation) *CapacityRejectionFacts {
	if observation.Component != ComponentResource || observation.Stage != StageResourceHard ||
		observation.Err == nil || observation.CapacityBudget == "" || observation.CapacityRejection == nil {
		return nil
	}
	f := *observation.CapacityRejection
	switch f.Phase {
	case "normal_input", "normal_gap", "normal_output", "query_free", "snapshot_prepare":
	default:
		f.Phase = "other"
	}
	if f.OwnUsed != nil {
		own := *f.OwnUsed
		f.OwnUsed = &own
	}
	return &f
}

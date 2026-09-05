package observability

// QueryFailureFacts is log-only: no raw error, query, URL or dimension values.
// Stage names the actual Coordinator boundary, not an inferred root cause.
type QueryFailureFacts struct{ Stage, Category, Code string }

func normalizeQueryFailure(component Component, stage Stage, err error, input *QueryFailureFacts) *QueryFailureFacts {
	if component != ComponentAccess || stage != StageQueryCompleted || err == nil || input == nil {
		return nil
	}
	f := *input
	switch f.Stage {
	case "execute", "stream_complete":
	default:
		f.Stage = "other"
	}
	switch f.Category {
	case "source_backend":
		if f.Code != "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS" {
			f.Code = "OTHER"
		}
	case "series_identity":
		if f.Code != "IDENTITY_FIELD_MISSING" {
			f.Code = "OTHER"
		}
	case "budget":
		f.Code = string(NormalizeCapacityBudget(CapacityBudget(f.Code)))
		if f.Code == "" {
			f.Code = "other"
		}
	default:
		f.Category = "other"
		f.Code = "OTHER"
	}
	return &f
}

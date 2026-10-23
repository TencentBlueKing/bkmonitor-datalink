package observability

// QueryCodecFacts is a log/metric projection of a completed physical response.
// OptIn+legacy_json explicitly means the same POST accepted a legacy response;
// it does not claim whether the server was old, disabled or chose fallback.
type QueryCodecFacts struct {
	Codec       string `json:"codec"`
	Negotiation string `json:"negotiation"`
	Completion  string `json:"completion"`
}

func normalizeQueryCodec(component Component, stage Stage, input []QueryCodecFacts) []QueryCodecFacts {
	if component != ComponentAccess || stage != StageQueryCompleted || len(input) == 0 {
		return nil
	}
	result := make([]QueryCodecFacts, 0, len(input))
	for _, fact := range input {
		switch fact.Codec {
		case "legacy_json", "shared_json_v1":
		default:
			fact.Codec = "other"
		}
		switch fact.Negotiation {
		case "disabled", "opt_in":
		default:
			fact.Negotiation = "other"
		}
		switch fact.Completion {
		case "full", "partial", "unavailable":
		default:
			fact.Completion = "other"
		}
		result = append(result, fact)
	}
	return result
}

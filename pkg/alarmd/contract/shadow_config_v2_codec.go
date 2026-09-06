package contract

import "encoding/json"

// DecodeComparisonConfigV2 distinguishes a present empty string/false from a
// missing or null field. Typed builders supply values from validated facts.
func DecodeComparisonConfigV2(b []byte, maxBytes int) (*ComparisonConfigV2, error) {
	var c ComparisonConfigV2
	if err := decodeShadowJSON(b, maxBytes, &c); err != nil {
		return nil, err
	}
	if err := checkConfigV2Fields(b, "config"); err != nil {
		return nil, err
	}
	if _, _, err := CanonicalComparisonConfigV2(c); err != nil {
		return nil, err
	}
	return &c, nil
}

func checkConfigV2Fields(b []byte, kind string) error {
	fieldsByKind := map[string][]string{
		"config":     {"schema_version", "primary_selection_mapping_version", "selection_order", "levels", "selector", "projection", "numeric", "effective_time", "schedule"},
		"level":      {"level_id", "priority", "connector", "detectors", "trigger", "recovery"},
		"detector":   {"kind", "mapping_version", "source_algorithm_family", "source_mapping_version", "operator", "threshold"},
		"trigger":    {"window_points", "required_anomalies", "step_seconds"},
		"recovery":   {"enabled", "consecutive_windows", "mode", "input_requirement"},
		"selector":   {"table", "metric", "aggregation", "step_millis", "query_alignment_millis", "timezone", "not_time_align", "filters", "filter_connectors", "data_source", "expression"},
		"filter":     {"field", "operator", "values"},
		"projection": {"value_fields", "identity_fields", "required_dimensions"},
		"numeric":    {"source_unit", "target_unit", "multiplier", "decimal_places", "rounding"},
		"schedule":   {"interval_seconds", "window_seconds", "alignment_seconds", "timezone"},
	}
	fields, err := validateJSONObjectFields(b, "shadow.config.v2."+kind, fieldsByKind[kind], nil, false)
	if err != nil {
		return err
	}
	for key, raw := range fields {
		if string(raw) == "null" {
			return invalid("shadow.config.v2."+key, "known value required")
		}
		child := ""
		array := false
		switch key {
		case "levels":
			child = "level"
			array = true
		case "detectors":
			child = "detector"
			array = true
		case "filters":
			child = "filter"
			array = true
		case "selector", "projection", "numeric", "schedule", "trigger", "recovery":
			child = key
		}
		if child == "" {
			continue
		}
		if !array {
			if err := checkConfigV2Fields(raw, child); err != nil {
				return err
			}
			continue
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := checkConfigV2Fields(value, child); err != nil {
				return err
			}
		}
	}
	return nil
}

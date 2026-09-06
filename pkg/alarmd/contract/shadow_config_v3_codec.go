package contract

import "encoding/json"

func DecodeComparisonConfigV3(b []byte, maxBytes int) (*ComparisonConfigV3, error) {
	var c ComparisonConfigV3
	if err := decodeShadowJSON(b, maxBytes, &c); err != nil {
		return nil, err
	}
	if err := checkConfigV3Fields(b, "config"); err != nil {
		return nil, err
	}
	if _, _, err := CanonicalComparisonConfigV3(c); err != nil {
		return nil, err
	}
	return &c, nil
}

func checkConfigV3Fields(b []byte, kind string) error {
	fieldsByKind := map[string][]string{
		"config":     {"schema_version", "primary_selection_mapping_version", "selection_order", "levels", "query", "projection", "numeric", "effective_time", "schedule"},
		"query":      {"selectors", "metric_merge", "step_millis", "alignment_millis", "timezone", "not_time_align", "down_sample_range"},
		"selector":   {"reference", "data_source", "driver", "table", "metric", "time_field", "is_regexp", "dimensions", "conditions", "functions", "time_aggregation", "offset", "offset_forward", "keep_columns", "query_string"},
		"conditions": {"fields", "connectors"},
		"condition":  {"field", "operator", "values", "wildcard", "prefix", "suffix"},
		"function":   {"method", "field", "without", "dimensions", "position", "arguments", "window", "subquery", "step"},
		"scalar":     {"kind", "value"},
	}
	fields, err := validateJSONObjectFields(b, "shadow.config.v3."+kind, fieldsByKind[kind], nil, false)
	if err != nil {
		return err
	}
	for key, raw := range fields {
		if string(raw) == "null" {
			if key == "time_aggregation" {
				continue
			}
			return invalid("shadow.config.v3."+key, "known field required")
		}
		child := ""
		array := false
		legacy := false
		switch key {
		case "query", "conditions":
			child = key
		case "selectors":
			child = "selector"
			array = true
		case "fields":
			child = "condition"
			array = true
		case "functions":
			child = "function"
			array = true
		case "time_aggregation":
			child = "function"
		case "arguments", "values":
			child = "scalar"
			array = true
		case "levels":
			child = "level"
			array = true
			legacy = true
		case "projection", "schedule":
			child = key
			legacy = true
		case "numeric":
			child = "numeric"
			legacy = true
			var c ComparisonConfigV3
			if err = json.Unmarshal(b, &c); err != nil {
				return err
			}
			for _, l := range c.Levels {
				for _, d := range l.Detectors {
					if d.Kind == "ProcPort" {
						child = "numeric_proc_port"
					}
				}
			}
		}
		if child == "" {
			if len(raw) > 0 && raw[0] == '[' {
				var entries []json.RawMessage
				if err = json.Unmarshal(raw, &entries); err != nil {
					return err
				}
				for _, entry := range entries {
					if string(entry) == "null" {
						return invalid("shadow.config.v3."+key, "known array item required")
					}
				}
			}
			continue
		}
		visit := func(value []byte) error {
			if legacy {
				return checkConfigV2Fields(value, child)
			}
			return checkConfigV3Fields(value, child)
		}
		if !array {
			if err = visit(raw); err != nil {
				return err
			}
			continue
		}
		var entries []json.RawMessage
		if err = json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if err = visit(entry); err != nil {
				return err
			}
		}
	}
	return nil
}

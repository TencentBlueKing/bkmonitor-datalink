// Tencent is pleased to support the open source community by making
// BlueKing available. Licensed under the MIT License.
package contract

import (
	"encoding/json"
	"sort"
	"strings"
)

// ShadowFilterV2 preserves ordered, string-valued source conditions.
type ShadowFilterV2 struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}
type ShadowDetectorConfigV2 struct {
	SourceAlgorithmFamily string `json:"source_algorithm_family"`
	SourceMappingVersion  string `json:"source_mapping_version"`
	Kind                  string `json:"kind"`
	MappingVersion        string `json:"mapping_version"`
	// Threshold uses the explicit predicate below. Other approved adapters
	// supply their versioned semantic config; no native fingerprint substitutes.
	Operator       string          `json:"operator,omitempty"`
	Threshold      string          `json:"threshold,omitempty"`
	SemanticConfig json.RawMessage `json:"semantic_config,omitempty"`
}
type ShadowTriggerConfigV2 struct {
	WindowPoints      uint32 `json:"window_points"`
	RequiredAnomalies uint32 `json:"required_anomalies"`
	StepSeconds       uint32 `json:"step_seconds"`
}
type ShadowRecoveryConfigV2 struct {
	Enabled            bool   `json:"enabled"`
	ConsecutiveWindows uint32 `json:"consecutive_windows"`
	Mode               string `json:"mode"`
	InputRequirement   string `json:"input_requirement"`
}
type ShadowLevelConfigV2 struct {
	LevelID   uint32                   `json:"level_id"`
	Priority  uint32                   `json:"priority"`
	Connector string                   `json:"connector"`
	Detectors []ShadowDetectorConfigV2 `json:"detectors"`
	Trigger   ShadowTriggerConfigV2    `json:"trigger"`
	Recovery  ShadowRecoveryConfigV2   `json:"recovery"`
}
type ShadowSelectorConfigV2 struct {
	Table                string           `json:"table"`
	Metric               string           `json:"metric"`
	Aggregation          string           `json:"aggregation"`
	StepMillis           int64            `json:"step_millis"`
	QueryAlignmentMillis int64            `json:"query_alignment_millis"`
	Timezone             string           `json:"timezone"`
	NotTimeAlign         bool             `json:"not_time_align"`
	Filters              []ShadowFilterV2 `json:"filters"`
	FilterConnectors     []string         `json:"filter_connectors"`
	DataSource           string           `json:"data_source"`
	Expression           string           `json:"expression"`
}
type ShadowProjectionConfigV2 struct {
	ValueFields        []string `json:"value_fields"`
	IdentityFields     []string `json:"identity_fields"`
	RequiredDimensions []string `json:"required_dimensions"`
}
type ShadowNumericConfigV2 struct {
	SourceUnit    string `json:"source_unit"`
	TargetUnit    string `json:"target_unit"`
	Multiplier    string `json:"multiplier"`
	DecimalPlaces uint32 `json:"decimal_places"`
	Rounding      string `json:"rounding"`
}
type ShadowScheduleConfigV2 struct {
	Timezone         string `json:"timezone"`
	IntervalSeconds  uint32 `json:"interval_seconds"`
	WindowSeconds    uint32 `json:"window_seconds"`
	AlignmentSeconds int64  `json:"alignment_seconds"`
}
type ComparisonConfigV2 struct {
	SchemaVersion           string                   `json:"schema_version"`
	SelectionMappingVersion string                   `json:"primary_selection_mapping_version"`
	SelectionOrder          []uint32                 `json:"selection_order"`
	Levels                  []ShadowLevelConfigV2    `json:"levels"`
	Selector                ShadowSelectorConfigV2   `json:"selector"`
	Projection              ShadowProjectionConfigV2 `json:"projection"`
	Numeric                 ShadowNumericConfigV2    `json:"numeric"`
	EffectiveTime           string                   `json:"effective_time"`
	Schedule                ShadowScheduleConfigV2   `json:"schedule"`
}

// CanonicalComparisonConfigV2 does not derive config from a production chain.
// The chain adapter must supply the actual effective semantic closure. First
// cohort supports ALWAYS effective time, as the current legacy compiler does.
func CanonicalComparisonConfigV2(input ComparisonConfigV2) ([]byte, string, error) {
	// Clone before sorting/normalizing; callers may retain their frozen input.
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	var c ComparisonConfigV2
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, "", err
	}
	if c.SchemaVersion != "comparison-config-v2" || c.SelectionMappingVersion == "" || len(c.Levels) == 0 || len(c.SelectionOrder) != len(c.Levels) || c.EffectiveTime != "ALWAYS" {
		return nil, "", invalid("shadow.config", "version, complete Levels and migrated effective time required")
	}
	ids := map[uint32]bool{}
	for i := range c.Levels {
		l := &c.Levels[i]
		if l.LevelID == 0 || l.Priority == 0 || ids[l.LevelID] || (l.Connector != "AND" && l.Connector != "OR") || len(l.Detectors) == 0 || l.Trigger.WindowPoints == 0 || l.Trigger.RequiredAnomalies == 0 || l.Trigger.RequiredAnomalies > l.Trigger.WindowPoints || l.Trigger.StepSeconds == 0 || l.Recovery.Mode != "CONTINUOUS_TRIGGER_MISS" || l.Recovery.InputRequirement != "DATA_DRIVEN" || (l.Recovery.Enabled && l.Recovery.ConsecutiveWindows == 0) {
			return nil, "", invalid("shadow.config.level", "incomplete or unmigrated Level config")
		}
		ids[l.LevelID] = true
		for j := range l.Detectors {
			d := &l.Detectors[j]
			if (d.MappingVersion != "canonical-threshold-v2" && d.MappingVersion != "canonical-threshold-dnf-v2") || d.SourceAlgorithmFamily != "" || d.SourceMappingVersion != "" {
				return nil, "", invalid("shadow.config.detector", "mapping version required")
			}
			if d.Kind == "Threshold" {
				if d.MappingVersion == "canonical-threshold-dnf-v2" {
					if d.Operator != "" || d.Threshold != "" {
						return nil, "", invalid("shadow.config.threshold", "ambiguous DNF predicate")
					}
					d.SemanticConfig, err = normalizeThresholdDNFV2(d.SemanticConfig)
					if err != nil {
						return nil, "", err
					}
					continue
				}
				if d.Operator != "GTE" && d.Operator != "GT" && d.Operator != "LTE" && d.Operator != "LT" && d.Operator != "EQ" && d.Operator != "NE" {
					return nil, "", invalid("shadow.config.threshold", "operator required")
				}
				d.Threshold, err = NormalizeShadowDecimalV1(d.Threshold)
				if err != nil {
					return nil, "", err
				}
				if len(d.SemanticConfig) != 0 {
					return nil, "", invalid("shadow.config.threshold", "ambiguous predicate")
				}
			} else {
				return nil, "", invalid("shadow.config.detector", "semantic adapter not yet implemented")
			}
		}
	}
	order := append([]ShadowLevelConfigV2(nil), c.Levels...)
	sort.Slice(order, func(i, j int) bool {
		if order[i].Priority == order[j].Priority {
			return order[i].LevelID < order[j].LevelID
		}
		return order[i].Priority < order[j].Priority
	})
	for i, l := range order {
		if c.SelectionOrder[i] != l.LevelID {
			return nil, "", invalid("shadow.config.selection", "selection order disagrees with mapped priority")
		}
	}
	sort.Slice(c.Levels, func(i, j int) bool { return c.Levels[i].LevelID < c.Levels[j].LevelID })
	if c.Selector.Metric == "" || c.Selector.Aggregation == "" || c.Selector.StepMillis <= 0 || c.Selector.QueryAlignmentMillis <= 0 || c.Selector.Timezone == "" || c.Selector.Expression == "" || c.Selector.FilterConnectors == nil || c.Schedule.Timezone == "" || c.Selector.Filters == nil || len(c.Projection.ValueFields) == 0 || c.Projection.IdentityFields == nil || c.Projection.RequiredDimensions == nil || c.Numeric.Rounding == "" || c.Schedule.IntervalSeconds == 0 || c.Schedule.WindowSeconds == 0 || c.Schedule.AlignmentSeconds < 0 {
		return nil, "", invalid("shadow.config", "selector, projection, numeric and Schedule facts required")
	}
	c.Numeric.Multiplier, err = NormalizeShadowDecimalV1(c.Numeric.Multiplier)
	if err != nil {
		return nil, "", err
	}
	for _, fields := range [][]string{c.Projection.IdentityFields, c.Projection.RequiredDimensions} {
		sort.Strings(fields)
		for i, f := range fields {
			if f == "" || (i > 0 && fields[i-1] == f) {
				return nil, "", invalid("shadow.config.projection", "empty or duplicate field")
			}
		}
	}
	valueFields := map[string]bool{}
	for _, field := range c.Projection.ValueFields {
		if field == "" || valueFields[field] {
			return nil, "", invalid("shadow.config.projection", "invalid value fields")
		}
		valueFields[field] = true
	}
	if c.Schedule.AlignmentSeconds >= int64(c.Schedule.IntervalSeconds) || c.Numeric.DecimalPlaces != 6 || c.Numeric.Rounding != "HALF_EVEN" || c.Numeric.Multiplier == "0" || strings.HasPrefix(c.Numeric.Multiplier, "-") {
		return nil, "", invalid("shadow.config", "unsupported schedule or numeric semantics")
	}
	if len(c.Selector.FilterConnectors) != max(0, len(c.Selector.Filters)-1) {
		return nil, "", invalid("shadow.config.filters", "missing connectors")
	}
	for _, connector := range c.Selector.FilterConnectors {
		if connector != "and" && connector != "or" {
			return nil, "", invalid("shadow.config.filters", "unsupported connector")
		}
	}
	for _, filter := range c.Selector.Filters {
		if filter.Field == "" || filter.Operator == "" || len(filter.Values) == 0 {
			return nil, "", invalid("shadow.config.filters", "incomplete filter")
		}
	}
	b, err := CanonicalJSONV2(c)
	if err != nil {
		return nil, "", err
	}
	digest, err := ShadowCanonicalDigestV1(json.RawMessage(b))
	return b, digest, err
}

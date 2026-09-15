// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

var shadowDecimalPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

func NormalizeShadowDecimalV1(v string) (string, error) {
	if !shadowDecimalPattern.MatchString(v) {
		return "", invalid("shadow.decimal", "finite decimal string required")
	}
	negative := strings.HasPrefix(v, "-")
	v = strings.TrimPrefix(v, "-")
	parts := strings.SplitN(v, ".", 2)
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	if fraction != "" {
		whole += "." + fraction
	}
	if negative && whole != "0" {
		whole = "-" + whole
	}
	return whole, nil
}

type ShadowDetectorConfigV1 struct {
	Kind           string `json:"kind"`
	MappingVersion string `json:"mapping_version"`
	// Threshold uses the explicit predicate below. Other approved adapters
	// supply their versioned semantic config; no native fingerprint substitutes.
	Operator       string          `json:"operator,omitempty"`
	Threshold      string          `json:"threshold,omitempty"`
	SemanticConfig json.RawMessage `json:"semantic_config,omitempty"`
}
type ShadowTriggerConfigV1 struct {
	WindowPoints      uint32 `json:"window_points"`
	RequiredAnomalies uint32 `json:"required_anomalies"`
	StepSeconds       uint32 `json:"step_seconds"`
}
type ShadowRecoveryConfigV1 struct {
	Enabled            bool   `json:"enabled"`
	ConsecutiveWindows uint32 `json:"consecutive_windows"`
	Mode               string `json:"mode"`
	InputRequirement   string `json:"input_requirement"`
}
type ShadowLevelConfigV1 struct {
	LevelID   uint32                   `json:"level_id"`
	Priority  uint32                   `json:"priority"`
	Connector string                   `json:"connector"`
	Detectors []ShadowDetectorConfigV1 `json:"detectors"`
	Trigger   ShadowTriggerConfigV1    `json:"trigger"`
	Recovery  ShadowRecoveryConfigV1   `json:"recovery"`
}
type ShadowSelectorConfigV1 struct {
	Table            string            `json:"table"`
	Metric           string            `json:"metric"`
	Aggregation      string            `json:"aggregation"`
	StepSeconds      uint32            `json:"step_seconds"`
	AlignmentSeconds int64             `json:"alignment_seconds"`
	Filters          []json.RawMessage `json:"filters"`
	DataSource       string            `json:"data_source,omitempty"`
	Expression       string            `json:"expression,omitempty"`
}
type ShadowProjectionConfigV1 struct {
	ValueFields        []string `json:"value_fields"`
	IdentityFields     []string `json:"identity_fields"`
	RequiredDimensions []string `json:"required_dimensions"`
}
type ShadowNumericConfigV1 struct {
	SourceUnit    string `json:"source_unit"`
	TargetUnit    string `json:"target_unit"`
	Multiplier    string `json:"multiplier"`
	DecimalPlaces uint32 `json:"decimal_places"`
	Rounding      string `json:"rounding"`
}
type ShadowScheduleConfigV1 struct {
	IntervalSeconds  uint32 `json:"interval_seconds"`
	WindowSeconds    uint32 `json:"window_seconds"`
	AlignmentSeconds int64  `json:"alignment_seconds"`
}
type ComparisonConfigV1 struct {
	SchemaVersion           string                   `json:"schema_version"`
	SelectionMappingVersion string                   `json:"primary_selection_mapping_version"`
	SelectionOrder          []uint32                 `json:"selection_order"`
	Levels                  []ShadowLevelConfigV1    `json:"levels"`
	Selector                ShadowSelectorConfigV1   `json:"selector"`
	Projection              ShadowProjectionConfigV1 `json:"projection"`
	Numeric                 ShadowNumericConfigV1    `json:"numeric"`
	EffectiveTime           string                   `json:"effective_time"`
	Schedule                ShadowScheduleConfigV1   `json:"schedule"`
}

// CanonicalComparisonConfigV1 does not derive config from a production chain.
// The chain adapter must supply the actual effective semantic closure. First
// cohort supports ALWAYS effective time, as the current legacy compiler does.
func CanonicalComparisonConfigV1(input ComparisonConfigV1) ([]byte, string, error) {
	// Clone before sorting/normalizing; callers may retain their frozen input.
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	var c ComparisonConfigV1
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, "", err
	}
	if c.SchemaVersion != "comparison-config-v1" || c.SelectionMappingVersion == "" || len(c.Levels) == 0 || len(c.SelectionOrder) != len(c.Levels) || c.EffectiveTime != "ALWAYS" {
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
			if d.MappingVersion == "" {
				return nil, "", invalid("shadow.config.detector", "mapping version required")
			}
			if d.Kind == "Threshold" {
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
	order := append([]ShadowLevelConfigV1(nil), c.Levels...)
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
	if c.Selector.Table == "" || c.Selector.Metric == "" || c.Selector.Aggregation == "" || c.Selector.StepSeconds == 0 || c.Selector.AlignmentSeconds < 0 || c.Selector.Filters == nil || len(c.Projection.ValueFields) == 0 || c.Projection.IdentityFields == nil || c.Projection.RequiredDimensions == nil || c.Numeric.SourceUnit == "" || c.Numeric.TargetUnit == "" || c.Numeric.Rounding == "" || c.Schedule.IntervalSeconds == 0 || c.Schedule.WindowSeconds == 0 || c.Schedule.AlignmentSeconds < 0 {
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
	b, err := CanonicalJSONV2(c)
	if err != nil {
		return nil, "", err
	}
	digest, err := ShadowCanonicalDigestV1(json.RawMessage(b))
	return b, digest, err
}

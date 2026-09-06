package contract

import "encoding/json"

// ComparisonConfigV3 retains ordered final query semantics in its own domain.
// It does not contain physical routing, credentials or chain-local revisions.
type ComparisonConfigV3 struct {
	SchemaVersion           string                   `json:"schema_version"`
	SelectionMappingVersion string                   `json:"primary_selection_mapping_version"`
	SelectionOrder          []uint32                 `json:"selection_order"`
	Levels                  []ShadowLevelConfigV2    `json:"levels"`
	Query                   ShadowQueryConfigV3      `json:"query"`
	Projection              ShadowProjectionConfigV2 `json:"projection"`
	Numeric                 ShadowNumericConfigV2    `json:"numeric"`
	EffectiveTime           string                   `json:"effective_time"`
	Schedule                ShadowScheduleConfigV2   `json:"schedule"`
}

type ShadowQueryConfigV3 struct {
	Selectors       []ShadowQuerySelectorV3 `json:"selectors"`
	MetricMerge     string                  `json:"metric_merge"`
	StepMillis      int64                   `json:"step_millis"`
	AlignmentMillis int64                   `json:"alignment_millis"`
	Timezone        string                  `json:"timezone"`
	NotTimeAlign    bool                    `json:"not_time_align"`
	DownSampleRange string                  `json:"down_sample_range"`
}

type ShadowQuerySelectorV3 struct {
	Reference       string                  `json:"reference"`
	DataSource      string                  `json:"data_source"`
	Driver          string                  `json:"driver"`
	Table           string                  `json:"table"`
	Metric          string                  `json:"metric"`
	TimeField       string                  `json:"time_field"`
	IsRegexp        bool                    `json:"is_regexp"`
	Dimensions      []string                `json:"dimensions"`
	Conditions      ShadowQueryConditionsV3 `json:"conditions"`
	Functions       []ShadowQueryFunctionV3 `json:"functions"`
	TimeAggregation *ShadowQueryFunctionV3  `json:"time_aggregation"`
	Offset          string                  `json:"offset"`
	OffsetForward   bool                    `json:"offset_forward"`
	KeepColumns     []string                `json:"keep_columns"`
	QueryString     string                  `json:"query_string"`
}

type ShadowQueryConditionsV3 struct {
	Fields     []ShadowQueryConditionV3 `json:"fields"`
	Connectors []string                 `json:"connectors"`
}

type ShadowQueryConditionV3 struct {
	Field    string                `json:"field"`
	Operator string                `json:"operator"`
	Values   []ShadowQueryScalarV3 `json:"values"`
	Wildcard string                `json:"wildcard"`
	Prefix   string                `json:"prefix"`
	Suffix   string                `json:"suffix"`
}

// Value is a JSON string for STRING and exact-decimal NUMBER, or a JSON bool
// for BOOLEAN. Kind retains the executed scalar type without float decoding.
type ShadowQueryScalarV3 struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

type ShadowQueryFunctionV3 struct {
	Method     string                `json:"method"`
	Field      string                `json:"field"`
	Without    bool                  `json:"without"`
	Dimensions []string              `json:"dimensions"`
	Position   int32                 `json:"position"`
	Arguments  []ShadowQueryScalarV3 `json:"arguments"`
	Window     string                `json:"window"`
	Subquery   bool                  `json:"subquery"`
	Step       string                `json:"step"`
}

func CanonicalComparisonConfigV3(input ComparisonConfigV3) ([]byte, string, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	var c ComparisonConfigV3
	if err = json.Unmarshal(payload, &c); err != nil {
		return nil, "", err
	}
	if c.SchemaVersion != "comparison-config-v3" {
		return nil, "", invalid("shadow.config.v3", "version required")
	}
	common := ComparisonConfigV2{SelectionMappingVersion: c.SelectionMappingVersion, SelectionOrder: c.SelectionOrder, Levels: c.Levels, Projection: c.Projection, Numeric: c.Numeric, EffectiveTime: c.EffectiveTime, Schedule: c.Schedule}
	if err = normalizeComparisonCommon(&common, false); err != nil {
		return nil, "", err
	}
	c.Levels, c.Projection, c.Numeric = common.Levels, common.Projection, common.Numeric
	if err = normalizeShadowQueryV3(&c.Query); err != nil {
		return nil, "", err
	}
	wire, err := CanonicalJSONV2(c)
	if err != nil {
		return nil, "", err
	}
	digest, err := ShadowCanonicalDigestV1(json.RawMessage(wire))
	return wire, digest, err
}

func normalizeShadowQueryV3(q *ShadowQueryConfigV3) error {
	fail := func() error { return invalid("shadow.config.v3.query", "complete executed query facts required") }
	if len(q.Selectors) == 0 || q.MetricMerge == "" || q.StepMillis <= 0 || q.AlignmentMillis <= 0 || q.Timezone == "" {
		return fail()
	}
	for i := range q.Selectors {
		s := &q.Selectors[i]
		if s.Reference == "" || s.Driver == "" || s.TimeField == "" || s.Dimensions == nil || s.Functions == nil || s.KeepColumns == nil || s.Conditions.Fields == nil || s.Conditions.Connectors == nil {
			return fail()
		}
		if len(s.Conditions.Connectors) != max(0, len(s.Conditions.Fields)-1) {
			return fail()
		}
		for _, v := range s.Conditions.Connectors {
			if v != "and" && v != "or" {
				return fail()
			}
		}
		for j := range s.Conditions.Fields {
			f := &s.Conditions.Fields[j]
			if f.Field == "" || f.Operator == "" || len(f.Values) == 0 {
				return fail()
			}
			for k := range f.Values {
				if err := normalizeShadowScalarV3(&f.Values[k]); err != nil {
					return err
				}
			}
		}
		for j := range s.Functions {
			if err := normalizeShadowFunctionV3(&s.Functions[j]); err != nil {
				return err
			}
		}
		if s.TimeAggregation != nil {
			if err := normalizeShadowFunctionV3(s.TimeAggregation); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeShadowFunctionV3(f *ShadowQueryFunctionV3) error {
	if f.Method == "" || f.Position < 0 || f.Dimensions == nil || f.Arguments == nil {
		return invalid("shadow.config.v3.function", "complete function facts required")
	}
	for i := range f.Arguments {
		if err := normalizeShadowScalarV3(&f.Arguments[i]); err != nil {
			return err
		}
	}
	return nil
}

func normalizeShadowScalarV3(v *ShadowQueryScalarV3) error {
	fail := func() error { return invalid("shadow.config.v3.scalar", "typed known value required") }
	if len(v.Value) == 0 || string(v.Value) == "null" {
		return fail()
	}
	switch v.Kind {
	case "STRING", "NUMBER":
		var s string
		if json.Unmarshal(v.Value, &s) != nil {
			return fail()
		}
		if v.Kind == "NUMBER" {
			n, err := NormalizeShadowDecimalV1(s)
			if err != nil {
				return err
			}
			s = n
		}
		v.Value, _ = json.Marshal(s)
	case "BOOLEAN":
		var b bool
		if json.Unmarshal(v.Value, &b) != nil {
			return fail()
		}
		v.Value, _ = json.Marshal(b)
	default:
		return fail()
	}
	return nil
}

package uq

import "encoding/json"

type request struct {
	QueryList       []queryClause `json:"query_list"`
	MetricMerge     string        `json:"metric_merge"`
	StartTime       string        `json:"start_time"`
	EndTime         string        `json:"end_time"`
	Step            string        `json:"step"`
	SpaceUID        string        `json:"space_uid"`
	DownSampleRange string        `json:"down_sample_range"`
	Timezone        string        `json:"timezone"`
	NotTimeAlign    bool          `json:"not_time_align"`
}

type queryClause struct {
	DataSource      string          `json:"data_source,omitempty"`
	TableID         string          `json:"table_id,omitempty"`
	FieldName       string          `json:"field_name,omitempty"`
	Driver          string          `json:"driver"`
	TimeField       string          `json:"time_field"`
	IsRegexp        bool            `json:"is_regexp"`
	ReferenceName   string          `json:"reference_name,omitempty"`
	Functions       []queryFunction `json:"function"`
	TimeAggregation timeAggregation `json:"time_aggregation"`
	Dimensions      []string        `json:"dimensions,omitempty"`
	Conditions      conditions      `json:"conditions,omitempty"`
	Offset          string          `json:"offset,omitempty"`
	OffsetForward   bool            `json:"offset_forward,omitempty"`
	KeepColumns     []string        `json:"keep_columns,omitempty"`
	QueryString     string          `json:"query_string"`
}

type timeAggregation struct {
	Function  string `json:"function,omitempty"`
	Window    string `json:"window,omitempty"`
	Position  int32  `json:"position"`
	VArgsList []any  `json:"vargs_list,omitempty"`
	Subquery  bool   `json:"is_sub_query,omitempty"`
	Step      string `json:"step,omitempty"`
}

type queryFunction struct {
	Method     string   `json:"method,omitempty"`
	Field      string   `json:"field,omitempty"`
	Without    bool     `json:"without,omitempty"`
	Dimensions []string `json:"dimensions,omitempty"`
	Position   int32    `json:"position"`
	VArgsList  []any    `json:"vargs_list,omitempty"`
	Window     string   `json:"window,omitempty"`
	Subquery   bool     `json:"is_sub_query,omitempty"`
	Step       string   `json:"step,omitempty"`
}

type conditions struct {
	Fields     []conditionField `json:"field_list,omitempty"`
	Connectors []string         `json:"condition_list,omitempty"`
}

type conditionField struct {
	Field    string   `json:"field_name"`
	Operator string   `json:"op"`
	Values   []string `json:"value"`
	Wildcard bool     `json:"is_wildcard,omitempty"`
	Prefix   bool     `json:"is_prefix,omitempty"`
	Suffix   bool     `json:"is_suffix,omitempty"`
}

type responseStatus struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responseSeries struct {
	Name        string              `json:"name"`
	Columns     []string            `json:"columns"`
	Types       []string            `json:"types"`
	GroupKeys   []string            `json:"group_keys"`
	GroupValues []string            `json:"group_values"`
	Values      [][]json.RawMessage `json:"values"`
}

package obevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Policies are allowlists for each configuration object, not a recursive
// blacklist over arbitrary source JSON. Unknown extensions never pass through.
type policy struct {
	fields     map[string]*policy
	values     *policy
	scalar     bool
	namedValue bool
}

var leaf = &policy{scalar: true}

func fields(names string, children map[string]*policy) *policy {
	p := &policy{fields: map[string]*policy{}}
	for _, name := range strings.Fields(names) {
		p.fields[name] = leaf
	}
	for name, child := range children {
		p.fields[name] = child
	}
	return p
}
func dictionary(value *policy) *policy { return &policy{values: value} }

func namedValues(p *policy) *policy { p.namedValue = true; return p }

var conditionPolicy = namedValues(fields("condition key method value field operator values keys group", nil))
var memberPolicy = fields("model_id model_inst_id bk_host_id bk_biz_id bk_obj_id bk_inst_id bcs_cluster_id namespace workload_kind workload_name node id ip bk_cloud_id", nil)
var targetPolicy = fields("schema_version model_id target_rule failure_policy static_keys dynamic_groups type selection_type target_type model_inst_ids", map[string]*policy{
	"identity":       fields("dimensions model_dimension model_value host_identity", nil),
	"static_members": memberPolicy, "static_targets": memberPolicy, "dynamic_topologies": memberPolicy,
	"groups": fields("", map[string]*policy{"conditions": conditionPolicy}), "conditions": conditionPolicy,
	"conditions_list": conditionPolicy, "nodes": memberPolicy, "hosts": memberPolicy,
})
var uptimePolicy = fields("is_enabled type timezone start end begin end_time begin_time week weekdays days months exclude_days include_days", map[string]*policy{
	"time_ranges": fields("start end begin end_time begin_time", nil),
	"date_ranges": fields("start end", nil),
})
var noDataPolicy = fields("is_enabled continuous level agg_dimension tracking_horizon_seconds", nil)
var algorithmConfigPolicy = func() *policy {
	p := fields("method threshold operator threshold_decimal value value_field data_unit threshold_unit_prefix ceil floor ceil_interval floor_interval ratio days count window window_size required_anomalies required_normals check_window trigger_count recovery_count threshold_count sensitivity period upper lower abs is_ratio multiplier comparison unit unit_prefix invalid_policy version floor_ratio ceil_ratio shock shock_unit", nil)
	p.fields["precision"] = fields("decimal_places rounding", nil)
	p.fields["groups"] = p
	p.fields["conditions"] = p
	p.fields["config"] = p
	return p
}()
var algorithmPolicy = fields("type version level unit_prefix", map[string]*policy{"config": algorithmConfigPolicy})
var triggerPolicy = fields("type version count check_window window_size required_anomalies required_normals", map[string]*policy{"uptime": uptimePolicy, "config": algorithmConfigPolicy})
var functionPolicy = fields("id method window dimensions without position field", map[string]*policy{"params": namedValues(fields("id value", nil))})
var sourceQueryPolicy = fields("alert_name index_set_id promql custom_event_name data_source_label data_type_label metric_id metric_field alias values agg_dimension agg_method agg_interval result_table_id time_field query_string data_label unit time_delay offset", map[string]*policy{"agg_condition": conditionPolicy, "functions": functionPolicy})
var sourcePolicy = fields("id bk_biz_id bk_tenant_id space_uid name is_enabled update_time strategy_revision priority priority_group_key labels scenario source type", map[string]*policy{
	"items": fields("id query_md5 expression time_delay unit", map[string]*policy{
		"query_configs": sourceQueryPolicy, "algorithms": algorithmPolicy, "functions": functionPolicy,
		"target":      namedValues(fields("condition key method value type model_id target_type", map[string]*policy{"conditions": conditionPolicy, "hosts": memberPolicy, "nodes": memberPolicy})),
		"target_plan": targetPolicy, "no_data_config": noDataPolicy,
	}),
	"detects":        fields("level priority connector", map[string]*policy{"trigger_config": triggerPolicy, "recovery_config": triggerPolicy, "effective_time": uptimePolicy}),
	"effective_time": uptimePolicy, "uptime": uptimePolicy,
})
var groupPolicy = fields("model_id model_inst_ids", map[string]*policy{"member_list": memberPolicy})

var scalarPolicy = fields("Kind StringValue NumberValue BoolValue", nil)
var frozenFunctionPolicy = fields("Method Field Without Dimensions Position Window Subquery Step", map[string]*policy{"Arguments": scalarPolicy})
var frozenConditionsPolicy = fields("Connectors", map[string]*policy{"Fields": namedValues(fields("Field Operator Wildcard Prefix Suffix", map[string]*policy{"Values": scalarPolicy}))})
var frozenClausePolicy = fields("FieldSemantics DataSource Driver TableID FieldName TimeField IsRegexp ReferenceName Dimensions Offset OffsetForward KeepColumns QueryString", map[string]*policy{
	"SourceConditions": frozenConditionsPolicy, "Conditions": frozenConditionsPolicy, "Functions": frozenFunctionPolicy, "TimeAggregation": frozenFunctionPolicy,
})
var frozenQueryPolicy = fields("QueryDelaySeconds SourceSemantics QueryRevision Provider ProviderRouteRef TenantID BusinessID SpaceScope MetricMerge StepMillis AlignmentMillis DownSampleRange Timezone NotTimeAlign", map[string]*policy{
	"QueryList": frozenClausePolicy, "PromQL": fields("Expression Match", nil),
	"TSDBMap": dictionary(fields("table_id storage_id storage_type db measurement need_add_time source_type", map[string]*policy{"time_field": fields("name type unit", nil)})),
})
var publishedPolicy = fields("object_contract_version query_group_identity query_group_schedule_revision membership_digest", map[string]*policy{
	"query_plan": frozenQueryPolicy,
	"plans": fields("state_generation plan_schedule_revision plan_id terminal_reason_code", map[string]*policy{
		"plan_identity": fields("TenantID BusinessID StrategyID", nil),
		"strategy":      fields("tenant_id strategy_id revision snapshot_revision", nil),
		"schedule_spec": fields("evaluation_interval alignment timezone completion_deadline_offset_seconds", nil),
		"query_plans":   dictionary(frozenQueryPolicy), "target_plan": targetPolicy, "target_scope": targetPolicy, "no_data": noDataPolicy,
		"strategy_ir": fields("schema required_features", map[string]*policy{
			"execution_semantics": fields("evaluation_interval lateness_tolerance", nil),
			"levels": fields("connector", map[string]*policy{
				"definition":   fields("level_id level_code priority", nil),
				"detect_plan":  fields("", map[string]*policy{"algorithms": algorithmPolicy}),
				"trigger_plan": triggerPolicy, "recovery_plan": triggerPolicy,
			}),
		}),
	}),
})

func projectJSON(raw []byte, p *policy) (any, []Omission, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, nil, errors.New("configuration must be an object")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, nil, errors.New("trailing JSON")
	}
	omitted := []Omission{}
	return project(value, p, "$", 0, &omitted), omitted, nil
}

func omit(out *[]Omission, path, reason string) {
	const maxOmissions = 128
	if len(*out) < maxOmissions {
		*out = append(*out, Omission{Path: path, Reason: reason})
	} else if len(*out) == maxOmissions {
		*out = append(*out, Omission{Path: "$", Reason: "additional_omitted_fields_not_listed"})
	}
}
func project(value any, p *policy, path string, depth int, omitted *[]Omission) any {
	if depth > 32 {
		omit(omitted, path, "projection_depth_limit")
		return nil
	}
	switch value := value.(type) {
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lowerKey := strings.ToLower(key)
			if p.namedValue && (lowerKey == "value" || lowerKey == "values" || lowerKey == "keys") && credentialParameter(value) {
				omit(omitted, path+"."+key, "credential_parameter")
				continue
			}
			child := p.fields[key]
			if child == nil {
				child = p.values
			}
			if child == nil {
				omit(omitted, path+"."+key, "field_not_exposed")
				continue
			}
			out[key] = project(value[key], child, path+"."+key, depth+1, omitted)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = project(item, p, path+"["+strconv.Itoa(i)+"]", depth+1, omitted)
		}
		return out
	default:
		if value == nil || p.scalar {
			return value
		}
		omit(omitted, path, "unexpected_value_shape")
		return nil
	}
}

// An allowed key/value-shaped condition or function parameter still does not
// permit a caller to rename an Authorization header into {id, value}.
func credentialParameter(value map[string]any) bool {
	for _, name := range []string{"key", "field", "id", "Field"} {
		text, _ := value[name].(string)
		switch strings.ToLower(text) {
		case "password", "passwd", "secret", "client_secret", "token", "access_token", "authorization", "header", "headers", "cookie":
			return true
		}
	}
	return false
}

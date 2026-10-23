package obevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Policies are allowlists for each configuration object, not a recursive
// blacklist over arbitrary source JSON. Unknown extensions never pass through
// as values. The source view shows their shape instead (projectSourceJSON).
type policy struct {
	fields     map[string]*policy
	values     *policy
	scalar     bool
	namedValue bool
	// guardKeys marks a dictionary whose keys are data - field names -
	// rather than a schema: a key named like a credential is left out.
	guardKeys bool
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

// guardedDictionary is a dictionary keyed by data, not by a schema; see
// policy.guardKeys.
func guardedDictionary(value *policy) *policy { return &policy{values: value, guardKeys: true} }

func namedValues(p *policy) *policy { p.namedValue = true; return p }

// scalarOr lets a plain value through where the policy otherwise expects an
// object: one field that the source document spells as an object and the
// frozen plan as a bare value reads in both.
func scalarOr(p *policy) *policy { p.scalar = true; return p }

var conditionPolicy = namedValues(fields("condition key method value field operator values keys group", nil))
var memberPolicy = fields("model_id model_inst_id bk_host_id bk_biz_id bk_obj_id bk_inst_id bcs_cluster_id namespace workload_kind workload_name node id ip bk_cloud_id", map[string]*policy{
	// A Kubernetes static target names what it matches here, as the writer
	// spells it; without it the target reads as a model and nothing else.
	"match": fields("bcs_cluster_id namespace node workload_kind workload_name", nil),
})
var targetPolicy = fields("schema_version model_id target_rule failure_policy static_keys exclude_keys exclude_hosts type selection_type target_type model_inst_ids bk_tenant_id static_hosts", map[string]*policy{
	"identity":       fields("dimensions model_dimension model_value host_identity address", nil),
	"static_members": memberPolicy, "static_targets": memberPolicy, "dynamic_topologies": memberPolicy,
	"exclude_members": memberPolicy, "exclude": memberPolicy,
	"groups": fields("", map[string]*policy{"conditions": conditionPolicy}), "conditions": conditionPolicy,
	"conditions_list": conditionPolicy, "nodes": memberPolicy, "hosts": memberPolicy,
	"static_businesses": dictionary(leaf),
	// The writer's model mapping for a model_inst_id rule: one dimension,
	// named by the writer, and the value the data carries in it.
	"model_match": guardedDictionary(leaf),
	// The writer spells each dynamic group as an object naming it, the frozen
	// plan as the bare id; as a leaf the source's read only while the writer
	// left the list empty.
	"dynamic_groups": scalarOr(fields("dynamic_group_id", nil)),
})
var uptimePolicy = fields("is_enabled type timezone start end begin end_time begin_time week weekdays days months exclude_days include_days calendars active_calendars", map[string]*policy{
	"time_ranges": fields("start end begin end_time begin_time", nil),
	"date_ranges": fields("start end", nil),
})
var noDataPolicy = fields("is_enabled continuous level agg_dimension tracking_horizon_seconds", nil)
var algorithmConfigPolicy = func() *policy {
	p := fields("method threshold operator threshold_decimal value value_field data_unit threshold_unit_prefix ceil floor ceil_interval floor_interval ratio days count window window_size required_anomalies required_normals check_window trigger_count recovery_count threshold_count sensitivity period upper lower abs is_ratio multiplier comparison unit unit_prefix invalid_policy version floor_ratio ceil_ratio shock shock_unit fetch_type", nil)
	p.fields["precision"] = fields("decimal_places rounding", nil)
	p.fields["groups"] = p
	p.fields["conditions"] = p
	p.fields["config"] = p
	return p
}()
var algorithmPolicy = fields("type version level unit_prefix", map[string]*policy{"config": algorithmConfigPolicy})
var triggerPolicy = fields("type version count check_window window_size required_anomalies required_normals", map[string]*policy{"uptime": uptimePolicy, "config": algorithmConfigPolicy})
var functionPolicy = fields("id method window dimensions without position field", map[string]*policy{"params": namedValues(fields("id value", nil))})
var sourceQueryPolicy = fields("alert_name index_set_id promql custom_event_name data_source_label data_type_label metric_id metric_field alias values agg_dimension agg_method agg_interval result_table_id time_field query_string data_label unit time_delay offset", map[string]*policy{
	"agg_condition": conditionPolicy, "functions": functionPolicy,
	// A PromQL query's label filter (field -> value or values), read by the
	// polling compiler as agg_condition is by the others: the same kind of
	// value, shown the same way, a field named like a credential left out.
	"filter_dict": guardedDictionary(leaf),
	// The object identity pair an object-model target is matched by.
	"target_identity": fields("type object_model_field object_model_inst_field", nil),
})

// targetValuePolicy is one value of a legacy target condition: a bare value,
// or the object naming a host, instance, node or group the compiler reads.
var targetValuePolicy = scalarOr(fields("bk_cloud_id bk_host_id bk_inst_id bk_obj_id bk_target_cloud_id bk_target_ip bk_target_service_instance_id dynamic_group_id ip service_instance_id cw_object_model_id cw_object_model_inst_id", nil))
var sourcePolicy = fields("id bk_biz_id bk_tenant_id space_uid is_global_strategy name is_enabled update_time strategy_revision priority priority_group_key labels scenario source type", map[string]*policy{
	"items": fields("id name query_md5 expression time_delay detect_interval unit", map[string]*policy{
		"query_configs": sourceQueryPolicy, "algorithms": algorithmPolicy, "functions": functionPolicy,
		"target":      namedValues(fields("condition key field method type model_id target_type", map[string]*policy{"value": targetValuePolicy, "conditions": conditionPolicy, "hosts": memberPolicy, "nodes": memberPolicy})),
		"target_plan": targetPolicy, "no_data_config": noDataPolicy,
	}),
	"detects":        fields("level priority connector", map[string]*policy{"trigger_config": triggerPolicy, "recovery_config": triggerPolicy, "effective_time": uptimePolicy}),
	"effective_time": uptimePolicy, "uptime": uptimePolicy, "effective_time_snapshot": effectiveSnapshotPolicy,
})
var groupPolicy = fields("model_id model_inst_ids", map[string]*policy{"member_list": memberPolicy})

// effectiveSnapshotPolicy is the writer's effective-time snapshot as the
// compiler reads it (strategy/effective_rules.go): its own status and reason
// are what decide a strategy withheld as EFFECTIVE_TIME_SNAPSHOT_*, and were
// the one part of that verdict no reader could see.
var effectiveSnapshotPolicy = fields("schema_version status reason business_timezone", map[string]*policy{
	"calendars": fields("id bk_tenant_id status", map[string]*policy{
		"items": fields("id time_kind start_time end_time time_zone parent_id", map[string]*policy{
			"repeat": fields("freq interval until every exclude_date exclude_date_encoding_timezone", nil),
		}),
	}),
})

var scalarPolicy = fields("Kind StringValue NumberValue BoolValue", nil)
var frozenFunctionPolicy = fields("Method Field Without Dimensions Position Window Subquery Step", map[string]*policy{"Arguments": scalarPolicy})
var frozenConditionsPolicy = fields("Connectors", map[string]*policy{"Fields": namedValues(fields("Field Operator Wildcard Prefix Suffix", map[string]*policy{"Values": scalarPolicy}))})
var frozenClausePolicy = fields("DataSource Driver TableID FieldName TimeField IsRegexp ReferenceName Dimensions Offset OffsetForward KeepColumns QueryString", map[string]*policy{
	"Conditions": frozenConditionsPolicy, "Functions": frozenFunctionPolicy, "TimeAggregation": frozenFunctionPolicy,
})
var frozenQueryPolicy = fields("QueryDelaySeconds SourceSemantics QueryRevision Provider ProviderRouteRef TenantID BusinessID SpaceScope GlobalBusiness MetricMerge StepMillis AlignmentMillis DownSampleRange Timezone NotTimeAlign", map[string]*policy{
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
	return projectDocument(raw, p, false)
}

// projectSourceJSON projects a strategy document the platform's writer
// wrote. A key its policy does not list is shown by its shape rather than
// left out: the name, and a value with every object key and array element in
// place, numbers, booleans and nulls as written, and each string as its
// length alone. A writer that adds a key this build does not know - an
// uptime's cw_calendars - is exactly what a refusal comes from, and a view
// that left the key out could not be replayed against the compiler; one that
// passed its strings through would pass whatever a key the policy never
// reviewed holds. A key named like a credential gives neither value nor
// shape, wherever it is.
func projectSourceJSON(raw []byte) (any, []Omission, error) {
	return projectDocument(raw, sourcePolicy, true)
}

func projectDocument(raw []byte, p *policy, shapeUnlisted bool) (any, []Omission, error) {
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
	return project(value, p, "$", 0, &omitted, shapeUnlisted), omitted, nil
}

func omit(out *[]Omission, path, reason string) {
	const maxOmissions = 128
	if len(*out) < maxOmissions {
		*out = append(*out, Omission{Path: path, Reason: reason})
	} else if len(*out) == maxOmissions {
		*out = append(*out, Omission{Path: "$", Reason: "additional_omitted_fields_not_listed"})
	}
}
func project(value any, p *policy, path string, depth int, omitted *[]Omission, shapeUnlisted bool) any {
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
			if p.guardKeys && credentialFieldName(key) {
				omit(omitted, path+"."+key, "credential_parameter")
				continue
			}
			lowerKey := strings.ToLower(key)
			if p.namedValue && (lowerKey == "value" || lowerKey == "values" || lowerKey == "keys") && credentialParameter(value) {
				omit(omitted, path+"."+key, "credential_parameter")
				continue
			}
			child := p.fields[key]
			if child == nil {
				child = p.values
			}
			if child == nil && shapeUnlisted {
				if credentialFieldName(key) {
					omit(omitted, path+"."+key, "credential_field")
					continue
				}
				omit(omitted, path+"."+key, "value_shape_only")
				out[key] = shapeOf(value[key], depth+1)
				continue
			}
			if child == nil {
				omit(omitted, path+"."+key, "field_not_exposed")
				continue
			}
			out[key] = project(value[key], child, path+"."+key, depth+1, omitted, shapeUnlisted)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = project(item, p, path+"["+strconv.Itoa(i)+"]", depth+1, omitted, shapeUnlisted)
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

// shapeOf is a value with its strings replaced by their length: objects keep
// their keys and arrays their elements, numbers, booleans and nulls are as
// written. A key named like a credential keeps its name and nothing else.
func shapeOf(value any, depth int) any {
	if depth > 32 {
		return "<beyond projection depth>"
	}
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			if credentialFieldName(key) {
				out[key] = "<credential field>"
				continue
			}
			out[key] = shapeOf(child, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for index, item := range value {
			out[index] = shapeOf(item, depth+1)
		}
		return out
	case string:
		return fmt.Sprintf("<string of %d bytes>", len(value))
	default:
		return value
	}
}

// credentialFieldName says whether a key is named like what holds a
// credential. Deliberately wide: a key it catches wrongly loses its shape, a
// key it misses would show a credential's length.
func credentialFieldName(key string) bool {
	lower := strings.ToLower(key)
	for _, word := range []string{"password", "passwd", "pwd", "pass", "secret", "token", "auth", "cookie", "header", "credential", "cert",
		"private_key", "privatekey", "api_key", "apikey", "access_key", "accesskey", "signature", "session"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
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

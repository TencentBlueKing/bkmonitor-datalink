// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The strategy item's monitoring target says which hosts, service instances,
// topology nodes or object-model instances the strategy is allowed to alert on. Python reduces it to
// identity keys once and then matches every record against them; this compiles
// the same reduction into the Plan.
//
// The reduction is deliberately a transcription of
// bkmonitor/utils/range/target.py rather than a re-derivation, including the
// parts that look like accidents:
//
//   - a condition whose values yield no keys is dropped, not turned into
//     "matches nothing";
//   - a group left with no conditions is dropped;
//   - a target that is present but reduces to no group at all matches nothing,
//     which is why an empty reduction is an error here instead of a Plan
//     without a scope.
type legacyTargetCondition struct {
	Field  string            `json:"field"`
	Method string            `json:"method"`
	Values []json.RawMessage `json:"value"`
}

type legacyTargetValue struct {
	BusinessObjectID  string      `json:"bk_obj_id"`
	InstanceID        json.Number `json:"bk_inst_id"`
	HostID            json.Number `json:"bk_host_id"`
	TargetIP          string      `json:"bk_target_ip"`
	IP                string      `json:"ip"`
	TargetCloudID     json.Number `json:"bk_target_cloud_id"`
	CloudID           json.Number `json:"bk_cloud_id"`
	ServiceInstanceID json.Number `json:"bk_target_service_instance_id"`
	InstanceIDAlias   json.Number `json:"service_instance_id"`
	DynamicGroupID    string      `json:"dynamic_group_id"`
}

// The fork's object-model targets identify a record by a pair of dimensions
// rather than by a CMDB lookup. Which pair is a property of the strategy: a
// query configuration may rename the identity through target_identity, and
// the platform's default pair applies beside whatever it names.
type legacyTargetIdentity struct {
	Type                 string `json:"type"`
	ObjectModelField     string `json:"object_model_field"`
	ObjectModelInstField string `json:"object_model_inst_field"`
}

type legacyQueryConfigIdentity struct {
	TargetIdentity *legacyTargetIdentity `json:"target_identity"`
}

const (
	defaultObjectModelField     = "cw_object_model_id"
	defaultObjectModelInstField = "cw_object_model_inst_id"
)

// compileTargetScope turns a strategy item's target into the frozen scope.
// A nil result means the item names no target, which is the only way a Plan
// may end up without one. The query configurations are read for the object
// identity pairs an OBJECT_MODEL_INST condition is matched by.
func compileTargetScope(target [][]legacyTargetCondition, queryConfigs []json.RawMessage) (*contract.TargetScopeV2, error) {
	// Python builds no target condition when the target is missing or its
	// first group is empty (item.py:123-126), whatever the groups after it
	// say: only target[0] is looked at.
	if len(target) == 0 || len(target[0]) == 0 {
		return nil, nil
	}

	scope := &contract.TargetScopeV2{}
	var identityPairs [][2]string
	for _, group := range target {
		compiled := contract.TargetScopeGroupV2{}
		for _, condition := range group {
			field, method, keys, err := compileTargetCondition(condition)
			if err != nil {
				return nil, err
			}
			// Python drops a condition that produced no keys instead of
			// letting it reject everything. Transcribed, not improved. A
			// dynamic group condition is the exception, in Python too: one
			// whose groups hold no host keeps the condition with a key that
			// matches nothing, so it is kept here with no group ids.
			if len(keys) == 0 && field != contract.TargetScopeDynamicGroup {
				continue
			}
			frozen := contract.TargetScopeConditionV2{Field: field, Method: method, Keys: keys}
			if field == contract.TargetScopeObjectModelInst {
				if identityPairs == nil {
					identityPairs = objectIdentityPairs(queryConfigs)
				}
				frozen.IdentityFields = append([][2]string(nil), identityPairs...)
			}
			compiled.Conditions = append(compiled.Conditions, frozen)
		}
		if len(compiled.Conditions) == 0 {
			continue
		}
		scope.Groups = append(scope.Groups, compiled)
	}
	if len(scope.Groups) == 0 {
		// The strategy states a target, yet nothing survives the reduction:
		// Python would then match no record at all. Publishing a Plan with no
		// scope would do the opposite - alert on everything - so the Plan is
		// rejected and the strategy stays on Python.
		return nil, fmt.Errorf("TARGET_SCOPE_UNRESOLVABLE: strategy states a monitoring target that reduces to no condition")
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return scope, nil
}

func compileTargetCondition(
	condition legacyTargetCondition,
) (contract.TargetScopeField, contract.TargetScopeMethod, []string, error) {
	// Python matches a condition as "eq" only when its method, lower-cased
	// and not trimmed, is exactly that, and as an exclusion otherwise
	// (target.py:41,147-150): "neq", an empty method, " eq" and any other
	// word all exclude.
	method := contract.TargetScopeExclude
	if strings.ToLower(condition.Method) == "eq" {
		method = contract.TargetScopeInclude
	}

	field := strings.ToLower(strings.TrimSpace(condition.Field))
	if field == "cw_object_model_inst" {
		// Read before the generic value decode below, which would report a
		// value that is not even an object as an unsupported target rather
		// than as the value shape it is.
		keys := make([]string, 0, len(condition.Values))
		for _, raw := range condition.Values {
			key, err := objectModelInstanceKey(raw)
			if err != nil {
				return "", "", nil, err
			}
			keys = append(keys, key)
		}
		return contract.TargetScopeObjectModelInst, method, contract.CanonicalTargetScopeKeys(keys), nil
	}
	values := make([]legacyTargetValue, 0, len(condition.Values))
	for _, raw := range condition.Values {
		var value legacyTargetValue
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return "", "", nil, fmt.Errorf("TARGET_SCOPE_UNSUPPORTED: decode target value: %w", err)
		}
		values = append(values, value)
	}

	switch {
	case field == "ip" || field == "bk_target_ip":
		keys := make([]string, 0, len(values)*2)
		for _, value := range values {
			if identifier := numberText(value.HostID); identifier != "" {
				keys = append(keys, identifier)
			}
			address := value.TargetIP
			if address == "" {
				address = value.IP
			}
			if address == "" {
				continue
			}
			cloud := numberText(value.TargetCloudID)
			if cloud == "" {
				cloud = numberText(value.CloudID)
			}
			if cloud == "" {
				cloud = "0"
			}
			keys = append(keys, address+"|"+cloud)
		}
		return contract.TargetScopeHost, method, contract.CanonicalTargetScopeKeys(keys), nil

	case field == "service_instance_id" || field == "bk_target_service_instance_id":
		keys := make([]string, 0, len(values))
		for _, value := range values {
			identifier := numberText(value.ServiceInstanceID)
			if identifier == "" {
				identifier = numberText(value.InstanceIDAlias)
			}
			if identifier == "" {
				continue
			}
			keys = append(keys, identifier)
		}
		return contract.TargetScopeServiceInstance, method, contract.CanonicalTargetScopeKeys(keys), nil

	case field == "host_topo_node" || field == "service_topo_node":
		// Both are matched against the topology nodes the record sits under;
		// for a service instance those are the nodes of its module, which the
		// service-instance fuller resolves. Python reads the same bk_topo_node
		// for either, and so does the matcher here.
		keys := make([]string, 0, len(values))
		for _, value := range values {
			instance := numberText(value.InstanceID)
			if value.BusinessObjectID == "" || instance == "" {
				continue
			}
			keys = append(keys, value.BusinessObjectID+"|"+instance)
		}
		return contract.TargetScopeTopoNode, method, contract.CanonicalTargetScopeKeys(keys), nil

	case field == "dynamic_group":
		// The platform's strategy cache keeps a dynamic group target as the
		// group ids (strategy.py insert_target) and Python reads the groups'
		// hosts from the CMDB cache when it matches (target.py), so the ids
		// are what is frozen and a Slot reads the members.
		keys := make([]string, 0, len(values))
		for _, value := range values {
			if value.DynamicGroupID != "" {
				keys = append(keys, value.DynamicGroupID)
			}
		}
		return contract.TargetScopeDynamicGroup, method, contract.CanonicalTargetScopeKeys(keys), nil

	case field == "cw_dynamic_group":
		// The fork's dynamic groups are object-model groups its strategy
		// cache writer expands into a target plan before writing; this field
		// arriving in a target is a group that writer did not expand.
		return "", "", nil, fmt.Errorf("TARGET_SCOPE_UNSUPPORTED: %s target must be expanded by the strategy cache writer before it is written", field)

	default:
		return "", "", nil, fmt.Errorf("TARGET_SCOPE_UNSUPPORTED: target field %q", condition.Field)
	}
}

// objectModelInstanceKey reads one cw_object_model_inst value into its
// "model|instance" key.
//
// The only shape accepted is the one the fork's own target builder reads: an
// object whose cw_object_model_id and cw_object_model_inst_id are both
// present scalars, and nothing else. Python skips a value that lacks either;
// this side refuses the strategy instead, under its own reason, because a
// skipped value is a target that silently names fewer objects than it was
// written to, and no other shape has been seen to exist. When one is, the
// refusal names the strategy and the reason says what to add here.
func objectModelInstanceKey(raw json.RawMessage) (string, error) {
	shape := func(detail string) error {
		return fmt.Errorf("TARGET_SCOPE_VALUE_SHAPE: cw_object_model_inst value must be an object holding exactly "+
			"%s and %s as non-empty scalars: %s", defaultObjectModelField, defaultObjectModelInstField, detail)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return "", shape("not a JSON object")
	}
	for name := range fields {
		if name != defaultObjectModelField && name != defaultObjectModelInstField {
			return "", shape(fmt.Sprintf("unexpected key %q", name))
		}
	}
	model, err := scalarText(fields[defaultObjectModelField])
	if err != nil || model == "" {
		return "", shape(defaultObjectModelField + " is missing or not a non-empty scalar")
	}
	instance, err := scalarText(fields[defaultObjectModelInstField])
	if err != nil || instance == "" {
		return "", shape(defaultObjectModelInstField + " is missing or not a non-empty scalar")
	}
	return model + "|" + instance, nil
}

// scalarText reads a JSON string or number as the text Python would format
// it as: strings trimmed, integral numbers without a fraction. A zero is a
// value here - an instance id may be 0 - which is why this does not share
// numberText's reading of zero as absent.
func scalarText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("absent")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text), nil
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return "", errors.New("not a scalar")
	}
	value := strings.TrimSpace(number.String())
	if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed == float64(int64(parsed)) {
		return strconv.FormatInt(int64(parsed), 10), nil
	}
	return value, nil
}

// objectIdentityPairs is the fork's iter_object_model_field_pairs without an
// aggregation-dimension filter: every query configuration's object_model_inst
// target_identity with both field names, first occurrence wins, and the
// platform's default pair last unless a configuration already named it.
func objectIdentityPairs(queryConfigs []json.RawMessage) [][2]string {
	pairs := make([][2]string, 0, 2)
	seen := make(map[[2]string]struct{}, 2)
	add := func(pair [2]string) {
		if _, duplicate := seen[pair]; duplicate {
			return
		}
		seen[pair] = struct{}{}
		pairs = append(pairs, pair)
	}
	for _, raw := range queryConfigs {
		var config legacyQueryConfigIdentity
		if err := json.Unmarshal(raw, &config); err != nil || config.TargetIdentity == nil {
			continue
		}
		identity := config.TargetIdentity
		if identity.Type != "object_model_inst" || identity.ObjectModelField == "" || identity.ObjectModelInstField == "" {
			continue
		}
		add([2]string{identity.ObjectModelField, identity.ObjectModelInstField})
	}
	add([2]string{defaultObjectModelField, defaultObjectModelInstField})
	return pairs
}

func numberText(value json.Number) string {
	text := strings.TrimSpace(value.String())
	if text == "" || text == "0" {
		return ""
	}
	if parsed, err := strconv.ParseFloat(text, 64); err == nil {
		if parsed == 0 {
			return ""
		}
		if parsed == float64(int64(parsed)) {
			return strconv.FormatInt(int64(parsed), 10)
		}
	}
	return text
}

// targetScopeDispositionReason maps a compilation failure onto the bounded
// reason the catalog publishes. The reason has to name the target, because the
// alternative reading - "this strategy is fine, it just failed once" - is what
// would keep the previous, unfiltered Plan alive.
func targetScopeDispositionReason(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "TARGET_SCOPE_UNRESOLVABLE") {
		return "UNSUPPORTED_TARGET_SCOPE_UNRESOLVABLE"
	}
	if strings.Contains(err.Error(), "TARGET_SCOPE_VALUE_SHAPE") {
		// A value this compiler could not read a key from. It is kept apart
		// from an unsupported field because the fix is different: a new
		// field needs a table row and a matcher source, a new value shape
		// needs a sample and one more reading in objectModelInstanceKey.
		return "UNSUPPORTED_TARGET_VALUE_SHAPE"
	}
	return "UNSUPPORTED_TARGET_SCOPE"
}

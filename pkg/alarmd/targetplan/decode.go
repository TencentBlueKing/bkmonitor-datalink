// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package targetplan reads the strategy cache's target_plan document into
// the frozen form the Plan carries, and later resolves that form's dynamic
// references into the members of one Slot.
//
// The decoder is strict and closed. The protocol names exactly five rules
// and exactly the fields each carries; anything else is refused with the
// path of the field that did not fit, and never read as the old target: a
// document that carries target_plan has chosen the new protocol, and a
// fallback to the old one would run the strategy on a target the writer
// did not mean.
package targetplan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The bounded reasons a target_plan is refused under. They are the
// disposition reasons the catalog publishes, so a refused strategy is
// listed by name on the first screen rather than folded into a generic
// rejection.
const (
	// ReasonUnsupported is a document this decoder cannot read: a missing or
	// extra field, a wrong type, an unknown version, an unknown rule. The
	// error names the field.
	ReasonUnsupported = "UNSUPPORTED_TARGET_PLAN"
	// ReasonEmpty is a well-formed plan that names no static target and no
	// dynamic reference. It would match nothing; refusing it puts it on the
	// first screen where a strategy that can never alert belongs.
	ReasonEmpty = "TARGET_PLAN_EMPTY"
)

// Error is one refusal: the bounded reason, the path inside target_plan of
// the field that decided it, and a detail for the log.
type Error struct {
	Reason string
	Path   string
	Detail string
}

func (err *Error) Error() string {
	if err.Path == "" {
		return err.Reason + ": " + err.Detail
	}
	return err.Reason + " at " + err.Path + ": " + err.Detail
}

func unsupported(path, format string, arguments ...any) *Error {
	return &Error{Reason: ReasonUnsupported, Path: path, Detail: fmt.Sprintf(format, arguments...)}
}

// The platform's default object-identity dimensions, as the fork's target
// builder reads them. They are the last identity pair when the query
// configurations name none.
const (
	DefaultObjectModelDimension    = "cw_object_model_id"
	DefaultObjectInstanceDimension = "cw_object_model_inst_id"
)

// Options are the strategy facts the decoder needs beside the document.
type Options struct {
	// ObjectIdentities are the (model dimension, instance dimension) pairs
	// the strategy's query configurations identify object-model records by,
	// first occurrence first and the platform default last. They decide how
	// a model_inst_id record key is read; nothing else reads them.
	ObjectIdentities [][2]string
}

// Decode reads one target_plan document. A nil error means the plan is
// frozen and valid; the frozen form passes contract validation by
// construction.
func Decode(raw json.RawMessage, options Options) (*contract.TargetPlanV1, *Error) {
	fields, err := objectFields(raw)
	if err != nil {
		return nil, unsupported("", "%s", err)
	}
	if err := onlyKeys(fields, "", "schema_version", "model_id", "target_rule", "failure_policy",
		"static_targets", "dynamic_groups", "dynamic_topologies", "model_match"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(fields["schema_version"])) != "1" {
		// Spelled exactly as the protocol spells it: not "1", not 1.0.
		return nil, unsupported("schema_version", "must be the integer 1")
	}
	modelID, err := nonEmptyText(fields["model_id"])
	if err != nil {
		return nil, unsupported("model_id", "%s", err)
	}
	ruleText, err := nonEmptyText(fields["target_rule"])
	if err != nil {
		return nil, unsupported("target_rule", "%s", err)
	}
	rule := contract.TargetPlanRule(ruleText)
	ruleDimensions, known := contract.TargetPlanRuleDimensions(rule)
	if !known {
		return nil, unsupported("target_rule", "unknown rule %q", ruleText)
	}
	policy, err := nonEmptyText(fields["failure_policy"])
	if err != nil || policy != contract.TargetPlanFailurePolicyNoMatch {
		return nil, unsupported("failure_policy", "must be %q", contract.TargetPlanFailurePolicyNoMatch)
	}

	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: modelID, Rule: rule, StaticKeys: []string{}}
	identity, err2 := decodeIdentity(rule, ruleDimensions, fields["model_match"], options)
	if err2 != nil {
		return nil, err2
	}
	plan.Identity = identity

	statics, err := arrayElements(fields["static_targets"])
	if err != nil {
		return nil, unsupported("static_targets", "%s", err)
	}
	keys := make([]string, 0, len(statics))
	seenMembers := make(map[contract.TargetPlanMemberV1]struct{}, len(statics))
	for index, element := range statics {
		key, member, err := decodeStaticTarget(rule, ruleDimensions, plan, element, fmt.Sprintf("static_targets[%d]", index))
		if err != nil {
			return nil, err
		}
		if member != nil {
			if _, duplicate := seenMembers[*member]; !duplicate {
				seenMembers[*member] = struct{}{}
				plan.StaticMembers = append(plan.StaticMembers, *member)
			}
			continue
		}
		keys = append(keys, key)
	}
	plan.StaticKeys = contract.CanonicalTargetScopeKeys(keys)
	contract.SortTargetPlanMembers(plan.StaticMembers)

	groups, err := arrayElements(fields["dynamic_groups"])
	if err != nil {
		return nil, unsupported("dynamic_groups", "%s", err)
	}
	if len(groups) > 0 && !contract.TargetPlanRuleAllowsDynamic(rule) {
		return nil, unsupported("dynamic_groups", "rule %s is static", rule)
	}
	groupIDs := make([]string, 0, len(groups))
	for index, element := range groups {
		path := fmt.Sprintf("dynamic_groups[%d]", index)
		members, err := objectFields(element)
		if err != nil {
			return nil, unsupported(path, "%s", err)
		}
		if err := onlyKeys(members, path, "dynamic_group_id"); err != nil {
			return nil, err
		}
		id, err := scalarText(members["dynamic_group_id"])
		if err != nil || id == "" {
			return nil, unsupported(path+".dynamic_group_id", "must be a non-empty id")
		}
		groupIDs = append(groupIDs, id)
	}
	if len(groupIDs) > 0 {
		plan.DynamicGroups = contract.CanonicalTargetScopeKeys(groupIDs)
	}

	topologies, err := arrayElements(fields["dynamic_topologies"])
	if err != nil {
		return nil, unsupported("dynamic_topologies", "%s", err)
	}
	if len(topologies) > 0 && !contract.TargetPlanRuleAllowsDynamic(rule) {
		return nil, unsupported("dynamic_topologies", "rule %s is static", rule)
	}
	seenNodes := make(map[string]struct{}, len(topologies))
	for index, element := range topologies {
		path := fmt.Sprintf("dynamic_topologies[%d]", index)
		members, err := objectFields(element)
		if err != nil {
			return nil, unsupported(path, "%s", err)
		}
		if err := onlyKeys(members, path, "bk_biz_id", "bk_obj_id", "bk_inst_id"); err != nil {
			return nil, err
		}
		business, err := integerText(members["bk_biz_id"])
		if err != nil {
			return nil, unsupported(path+".bk_biz_id", "%s", err)
		}
		object, err := nonEmptyText(members["bk_obj_id"])
		if err != nil {
			return nil, unsupported(path+".bk_obj_id", "%s", err)
		}
		instance, err := integerText(members["bk_inst_id"])
		if err != nil {
			return nil, unsupported(path+".bk_inst_id", "%s", err)
		}
		node := contract.TargetPlanTopologyV1{BusinessID: business, ObjectID: object, InstanceID: instance}
		if _, duplicate := seenNodes[node.Key()]; duplicate {
			continue
		}
		seenNodes[node.Key()] = struct{}{}
		plan.DynamicTopologies = append(plan.DynamicTopologies, node)
	}
	contract.SortTargetPlanTopologies(plan.DynamicTopologies)

	if len(plan.StaticKeys) == 0 && len(plan.StaticMembers) == 0 && len(plan.DynamicGroups) == 0 && len(plan.DynamicTopologies) == 0 {
		return nil, &Error{Reason: ReasonEmpty, Detail: "the plan names no static target and no dynamic reference"}
	}
	if err := plan.Validate(); err != nil {
		// Unreachable by construction; kept so a decoder change that breaks
		// the frozen form is a refusal, not a Plan.
		return nil, unsupported("", "%s", err)
	}
	return plan, nil
}

// decodeIdentity decides how a record key is read for the rule.
//
// The fixed-dimension rules read their dimensions and nothing else; host_id
// reads the record's host identity, which is bk_host_id when the record
// carries it and the id the host cache teaches it otherwise. The
// model_inst_id rule reads the data's representation of the plan's model,
// and the strategy can say what that is in two ways: the writer names it
// (model_match: the dimension and the value the data carries for this
// plan's model, so the key is the instance alone behind a model gate), or a
// query configuration names an identity pair whose model dimension is not
// the platform default, which is the fork's way of pointing at a dimension
// that carries the model code (so the key is "code|instance" and compares
// with the members as written). With neither, the members are read as
// hosts: a host's canonical identity is (model, instance) as well, the
// protocol names hosts that way on every collected metric and never sends a
// model_match for them, and the host cache is what says whether a member is
// a host. So the plan is frozen as read by host identity, and the worker's
// resolution names, per Slot, the members the cache does not know.
func decodeIdentity(
	rule contract.TargetPlanRule, ruleDimensions []string, modelMatch json.RawMessage, options Options,
) (contract.TargetPlanIdentityV1, *Error) {
	if rule != contract.TargetPlanRuleModelInstID {
		if len(modelMatch) != 0 {
			return contract.TargetPlanIdentityV1{}, unsupported("model_match", "only %s carries a model match", contract.TargetPlanRuleModelInstID)
		}
		return contract.TargetPlanIdentityV1{Dimensions: ruleDimensions, HostIdentity: rule == contract.TargetPlanRuleHostID}, nil
	}
	pairs := options.ObjectIdentities
	if len(pairs) == 0 {
		pairs = [][2]string{{DefaultObjectModelDimension, DefaultObjectInstanceDimension}}
	}
	if len(modelMatch) != 0 {
		fields, err := objectFields(modelMatch)
		if err != nil {
			return contract.TargetPlanIdentityV1{}, unsupported("model_match", "%s", err)
		}
		if len(fields) != 1 {
			return contract.TargetPlanIdentityV1{}, unsupported("model_match", "names exactly one dimension and its value")
		}
		var dimension string
		var raw json.RawMessage
		for name, value := range fields {
			dimension, raw = name, value
		}
		value, err := scalarText(raw)
		if err != nil || value == "" {
			return contract.TargetPlanIdentityV1{}, unsupported("model_match."+dimension, "must be the non-empty value the data carries")
		}
		for _, pair := range pairs {
			if pair[0] == dimension {
				return contract.TargetPlanIdentityV1{Dimensions: []string{pair[1]}, ModelDimension: dimension, ModelValue: value}, nil
			}
		}
		return contract.TargetPlanIdentityV1{}, unsupported("model_match."+dimension, "no identity pair of this strategy reads that dimension as the model")
	}
	for _, pair := range pairs {
		if pair[0] != DefaultObjectModelDimension {
			return contract.TargetPlanIdentityV1{Dimensions: []string{pair[0], pair[1]}}, nil
		}
	}
	return contract.TargetPlanIdentityV1{Dimensions: []string{contract.HostIdentityDimension}, HostIdentity: true}, nil
}

// decodeStaticTarget reads one static target into its member key, or, on a
// model_inst_id plan read by host identity, into the (model, instance)
// member the worker maps to a host id once per Slot.
func decodeStaticTarget(
	rule contract.TargetPlanRule, ruleDimensions []string, plan *contract.TargetPlanV1, element json.RawMessage, path string,
) (string, *contract.TargetPlanMemberV1, *Error) {
	fields, err := objectFields(element)
	if err != nil {
		return "", nil, unsupported(path, "%s", err)
	}
	switch rule {
	case contract.TargetPlanRuleHostID:
		if err := onlyKeys(fields, path, "bk_host_id"); err != nil {
			return "", nil, err
		}
		host, err := integerText(fields["bk_host_id"])
		if err != nil {
			return "", nil, unsupported(path+".bk_host_id", "%s", err)
		}
		return contract.TargetPlanMemberKey(host), nil, nil
	case contract.TargetPlanRuleModelInstID:
		if err := onlyKeys(fields, path, "model_id", "model_inst_id"); err != nil {
			return "", nil, err
		}
		model, instance, err := memberModelInstance(fields, path, plan.ModelID)
		if err != nil {
			return "", nil, err
		}
		if plan.Identity.HostIdentity {
			return "", &contract.TargetPlanMemberV1{ModelID: model, ModelInstID: instance}, nil
		}
		return plan.Identity.MemberKey(model, instance), nil, nil
	default:
		if err := onlyKeys(fields, path, "model_id", "model_inst_id", "match"); err != nil {
			return "", nil, err
		}
		if _, _, err := memberModelInstance(fields, path, plan.ModelID); err != nil {
			return "", nil, err
		}
		match, err2 := objectFields(fields["match"])
		if err2 != nil {
			return "", nil, unsupported(path+".match", "%s", err2)
		}
		if err := onlyKeys(match, path+".match", ruleDimensions...); err != nil {
			return "", nil, err
		}
		parts := make([]string, 0, len(ruleDimensions))
		for _, dimension := range ruleDimensions {
			value, err := nonEmptyText(match[dimension])
			if err != nil {
				return "", nil, unsupported(path+".match."+dimension, "%s", err)
			}
			parts = append(parts, value)
		}
		return contract.TargetPlanMemberKey(parts...), nil, nil
	}
}

func memberModelInstance(fields map[string]json.RawMessage, path, planModel string) (string, string, *Error) {
	model, err := nonEmptyText(fields["model_id"])
	if err != nil {
		return "", "", unsupported(path+".model_id", "%s", err)
	}
	if model != planModel {
		return "", "", unsupported(path+".model_id", "%q is not the plan's model %q", model, planModel)
	}
	instance, err := scalarText(fields["model_inst_id"])
	if err != nil || instance == "" {
		return "", "", unsupported(path+".model_inst_id", "must be a non-empty scalar")
	}
	return model, instance, nil
}

func objectFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("is absent")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, fmt.Errorf("must be a JSON object")
	}
	return fields, nil
}

func arrayElements(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("is absent")
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil || elements == nil {
		return nil, fmt.Errorf("must be a JSON array")
	}
	return elements, nil
}

// onlyKeys refuses a field the table does not name and a named field that
// is absent, each by path. Optional fields are not a concept here: every
// field the protocol lists is required, and model_match is the one
// exception, checked by its reader.
func onlyKeys(fields map[string]json.RawMessage, path string, allowed ...string) *Error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		found := false
		for _, candidate := range allowed {
			if candidate == name {
				found = true
				break
			}
		}
		if !found {
			return unsupported(join(path, name), "is not a field of the protocol")
		}
	}
	for _, name := range allowed {
		if name == "model_match" {
			continue
		}
		if _, present := fields[name]; !present {
			return unsupported(join(path, name), "is required")
		}
	}
	return nil
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// integerText reads a positive integer given as a JSON number or as digit
// text, as the text of that integer. Zero and negatives are not identities.
func integerText(raw json.RawMessage) (string, error) {
	value, err := scalarText(raw)
	if err != nil {
		return "", err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return "", fmt.Errorf("must be a positive integer")
	}
	return strconv.FormatInt(parsed, 10), nil
}

func nonEmptyText(raw json.RawMessage) (string, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "", fmt.Errorf("is absent")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("must be a string")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("must be non-empty")
	}
	return strings.TrimSpace(text), nil
}

// scalarText reads a JSON string or number as text: strings trimmed,
// integral numbers without a fraction. It is the reading the platform's
// own values need, where an id arrives as 101 in one document and "101" in
// the next.
func scalarText(raw json.RawMessage) (string, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "", fmt.Errorf("is absent")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text), nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return "", fmt.Errorf("must be a string or a number")
	}
	value := strings.TrimSpace(number.String())
	if !strings.ContainsAny(value, ".eE") {
		// An integer literal is its own text, however long: nothing on a key
		// path rounds an id through a float.
		return value, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed != float64(int64(parsed)) || parsed > 1<<53 || parsed < -(1<<53) {
		return "", fmt.Errorf("must be an integer")
	}
	return strconv.FormatInt(int64(parsed), 10), nil
}

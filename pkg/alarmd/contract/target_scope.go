// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A strategy item carries a monitoring target: the hosts, service instances,
// topology nodes or object-model instances it is allowed to alert on. Python
// resolves it in the access chain and drops every record outside it, so a plan
// that ignores it alerts on machines the strategy was never pointed at.
//
// The scope is frozen into the Plan rather than left in the legacy document,
// because a Plan that silently carries no scope is indistinguishable from a
// Plan whose strategy has none. Compilation either produces a scope or rejects
// the Plan; there is no third outcome in which the filter quietly disappears.
type TargetScopeV2 struct {
	Groups []TargetScopeGroupV2 `json:"groups"`
}

// Groups are alternatives: a record inside any one of them is in scope.
type TargetScopeGroupV2 struct {
	Conditions []TargetScopeConditionV2 `json:"conditions"`
}

// Conditions inside a group all have to hold.
type TargetScopeConditionV2 struct {
	Field  TargetScopeField  `json:"field"`
	Method TargetScopeMethod `json:"method"`
	// Keys are canonical identities, already reduced from the strategy's
	// wire form: "obj|inst" for topology nodes, "ip|cloud" and the bare host
	// id for hosts, the bare id for service instances, "model|inst" for
	// object-model instances.
	Keys []string `json:"keys"`
	// IdentityFields names the dimension pairs an OBJECT_MODEL_INST record is
	// identified by: each pair is (model dimension, instance dimension), and
	// a record yields one candidate key per pair it carries. The pairs are a
	// property of the strategy (its query configurations may rename the
	// identity), so they are frozen here rather than looked up at match time.
	// Only that field carries them; on any other field they are a
	// validation failure. Omitted from the wire when absent so the digest of
	// every scope built before this field existed is unchanged.
	IdentityFields [][2]string `json:"identity_fields,omitempty"`
}

type TargetScopeField string

const (
	// TargetScopeTopoNode covers host_topo_node and service_topo_node: both
	// are matched against the topology nodes a record belongs to. For a
	// service instance those are the nodes of the instance's module, which
	// the service-instance fuller resolves.
	TargetScopeTopoNode TargetScopeField = "TOPO_NODE"
	// TargetScopeHost covers ip / bk_target_ip. A dynamic group of hosts also
	// reduces to host identities in Python, and used to be named here as if it
	// did so on this side too; the compiler refuses that target shape outright,
	// so no scope this package holds was ever built from one.
	TargetScopeHost TargetScopeField = "HOST"
	// TargetScopeServiceInstance covers service_instance_id.
	TargetScopeServiceInstance TargetScopeField = "SERVICE_INSTANCE"
	// TargetScopeObjectModelInst covers cw_object_model_inst: an instance of
	// an object model the platform's fork defines beside hosts. The record
	// identifies itself by dimension values, so there is no cache to resolve
	// against; the key is built from the dimensions the condition names.
	TargetScopeObjectModelInst TargetScopeField = "OBJECT_MODEL_INST"
)

type TargetScopeMethod string

const (
	TargetScopeInclude TargetScopeMethod = "EQ"
	TargetScopeExclude TargetScopeMethod = "NEQ"
)

// The attributes a record can be matched on, one per field. The matcher is
// generic over this table: it reads the attribute the field names, applies the
// field's absence policy when the record has no candidate, and compares
// candidates with keys. Adding a way to target records is therefore a row here
// plus a compiler branch, plus a fuller when the attribute comes from a new
// source; the matching code does not change.
const (
	// AttributeHostIdentity holds "ip|cloud" and the bare host id.
	AttributeHostIdentity = "host.identity"
	// AttributeServiceInstanceID holds the bare service-instance id.
	AttributeServiceInstanceID = "service_instance.id"
	// AttributeHostTopoNode holds the "obj|inst" nodes the record sits under.
	AttributeHostTopoNode = "host.topo_node"
	// AttributeHostPrefix is where the CMDB host fuller exposes the scalar
	// attributes of the resolved host, one per field: "host.attr.bk_state",
	// "host.attr.bk_os_type" and so on. No field reads them yet; a target on a
	// host attribute is a row in the table naming one of them.
	AttributeHostPrefix = "host.attr."
)

// TargetScopeAttributeSource says where a field's candidates come from.
type TargetScopeAttributeSource string

const (
	// TargetScopeSourceFacts reads the named attribute the fullers filled.
	TargetScopeSourceFacts TargetScopeAttributeSource = "FACTS"
	// TargetScopeSourceDimensionPairs builds "first|second" keys from the
	// record's own dimensions, one per pair the condition's IdentityFields
	// names. Nothing is filled ahead of time because the pairs belong to the
	// strategy, not to the record.
	TargetScopeSourceDimensionPairs TargetScopeAttributeSource = "DIMENSION_PAIRS"
)

// TargetScopeAbsence is what a condition does when the record has no
// candidate for its attribute. Both policies are Python's: a host condition
// it cannot evaluate is skipped and the rest of the group decides, while a
// topology or object-identity condition without a candidate ends the group.
type TargetScopeAbsence string

const (
	TargetScopeAbsenceSkipCondition TargetScopeAbsence = "SKIP_CONDITION"
	TargetScopeAbsenceFailGroup     TargetScopeAbsence = "FAIL_GROUP"
)

// TargetScopeAttribute is one row of the table.
type TargetScopeAttribute struct {
	Field     TargetScopeField
	Attribute string
	Source    TargetScopeAttributeSource
	Absence   TargetScopeAbsence
	// AbsenceReason is the rejection reason when every alternative failed
	// because this attribute was absent; it is empty for a field whose absence
	// only skips the condition. MismatchReason is the reason when every
	// alternative failed on this attribute having candidates that did not
	// match. A rejection that fails on different attributes across its
	// alternatives is reported as plain out_of_scope.
	AbsenceReason  string
	MismatchReason string
}

const (
	// TargetScopeReasonOutOfScope is the ordinary rejection.
	TargetScopeReasonOutOfScope = "out_of_scope"
	// TargetScopeReasonObjectIdentityMissing says the record carried none of
	// the dimension pairs the strategy identifies its objects by. It is a
	// defect somewhere - the writer did not reduce a host-family target, the
	// query renamed the identity, or the pairs name dimensions the data does
	// not have - and never a legitimate out-of-scope record, so it is named
	// apart and reported with the pairs expected and the dimensions seen.
	TargetScopeReasonObjectIdentityMissing = "object_identity_missing"
	// TargetScopeReasonObjectIdentityUnmatched says the record built an object
	// identity and the target named none of them. That is what a record
	// outside the target looks like, and also what a target written in one
	// representation (a model code) against data in another (a model id)
	// looks like; the two are told apart only by seeing both sides, so the
	// report carries samples of each.
	TargetScopeReasonObjectIdentityUnmatched = "object_identity_unmatched"
)

var targetScopeAttributes = map[TargetScopeField]TargetScopeAttribute{
	TargetScopeHost: {
		Field: TargetScopeHost, Attribute: AttributeHostIdentity, Source: TargetScopeSourceFacts,
		Absence: TargetScopeAbsenceSkipCondition, MismatchReason: TargetScopeReasonOutOfScope,
	},
	TargetScopeServiceInstance: {
		Field: TargetScopeServiceInstance, Attribute: AttributeServiceInstanceID, Source: TargetScopeSourceFacts,
		Absence: TargetScopeAbsenceSkipCondition, MismatchReason: TargetScopeReasonOutOfScope,
	},
	TargetScopeTopoNode: {
		Field: TargetScopeTopoNode, Attribute: AttributeHostTopoNode, Source: TargetScopeSourceFacts,
		Absence: TargetScopeAbsenceFailGroup, AbsenceReason: TargetScopeReasonOutOfScope,
		MismatchReason: TargetScopeReasonOutOfScope,
	},
	TargetScopeObjectModelInst: {
		Field: TargetScopeObjectModelInst, Source: TargetScopeSourceDimensionPairs,
		Absence: TargetScopeAbsenceFailGroup, AbsenceReason: TargetScopeReasonObjectIdentityMissing,
		MismatchReason: TargetScopeReasonObjectIdentityUnmatched,
	},
}

// TargetScopeAttributeFor returns the table row for a field, and false for a
// field the table does not know. The matcher fails a group on an unknown
// field rather than skipping it: a condition nobody can evaluate must not
// widen the target.
func TargetScopeAttributeFor(field TargetScopeField) (TargetScopeAttribute, bool) {
	attribute, known := targetScopeAttributes[field]
	return attribute, known
}

// TargetScopeFields lists the fields the table knows, in a stable order, for
// tests and reports.
func TargetScopeFields() []TargetScopeField {
	fields := make([]TargetScopeField, 0, len(targetScopeAttributes))
	for field := range targetScopeAttributes {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(left, right int) bool { return fields[left] < fields[right] })
	return fields
}

func (scope *TargetScopeV2) Validate() error {
	if scope == nil {
		return nil
	}
	if len(scope.Groups) == 0 {
		return errors.New("alarmd contract: a target scope with no alternative matches nothing and must be absent instead")
	}
	for _, group := range scope.Groups {
		if len(group.Conditions) == 0 {
			return errors.New("alarmd contract: a target scope group needs at least one condition")
		}
		for _, condition := range group.Conditions {
			if err := condition.validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (condition TargetScopeConditionV2) validate() error {
	attribute, known := TargetScopeAttributeFor(condition.Field)
	if !known {
		return fmt.Errorf("alarmd contract: unknown target scope field %q", condition.Field)
	}
	switch condition.Method {
	case TargetScopeInclude, TargetScopeExclude:
	default:
		return fmt.Errorf("alarmd contract: unknown target scope method %q", condition.Method)
	}
	switch attribute.Source {
	case TargetScopeSourceDimensionPairs:
		if len(condition.IdentityFields) == 0 {
			return fmt.Errorf("alarmd contract: a %s condition needs the dimension pairs its records are identified by", condition.Field)
		}
		seen := make(map[[2]string]struct{}, len(condition.IdentityFields))
		for _, pair := range condition.IdentityFields {
			if strings.TrimSpace(pair[0]) == "" || strings.TrimSpace(pair[1]) == "" {
				return fmt.Errorf("alarmd contract: a %s identity pair must name both dimensions", condition.Field)
			}
			if _, duplicate := seen[pair]; duplicate {
				return fmt.Errorf("alarmd contract: %s identity pairs must be unique", condition.Field)
			}
			seen[pair] = struct{}{}
		}
	default:
		if len(condition.IdentityFields) != 0 {
			return fmt.Errorf("alarmd contract: identity pairs are only meaningful on %s, not %s", TargetScopeObjectModelInst, condition.Field)
		}
	}
	// An empty include list matches nothing and an empty exclude list
	// constrains nothing. Both are real states in production - a topology node
	// can legitimately hold no host - but they must be stated by the compiler
	// as an empty key list, never by omitting the condition, or the filter
	// turns itself off exactly where it matters most.
	for _, key := range condition.Keys {
		if strings.TrimSpace(key) == "" {
			return errors.New("alarmd contract: target scope keys must be non-empty canonical text")
		}
	}
	if !sort.StringsAreSorted(condition.Keys) {
		return errors.New("alarmd contract: target scope keys must be canonically ordered")
	}
	for index := 1; index < len(condition.Keys); index++ {
		if condition.Keys[index] == condition.Keys[index-1] {
			return errors.New("alarmd contract: target scope keys must be unique")
		}
	}
	return nil
}

// CanonicalTargetScopeKeys sorts and de-duplicates in place so the same scope
// always digests the same way regardless of the order CMDB returned.
func CanonicalTargetScopeKeys(keys []string) []string {
	if len(keys) == 0 {
		return []string{}
	}
	unique := make(map[string]struct{}, len(keys))
	canonical := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, seen := unique[key]; seen {
			continue
		}
		unique[key] = struct{}{}
		canonical = append(canonical, key)
	}
	sort.Strings(canonical)
	return canonical
}

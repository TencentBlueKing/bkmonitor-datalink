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

// A strategy item carries a monitoring target: the hosts, service instances or
// topology nodes it is allowed to alert on. Python resolves it in the access
// chain and drops every record outside it, so a plan that ignores it alerts on
// machines the strategy was never pointed at.
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
	// id for hosts, the bare id for service instances.
	Keys []string `json:"keys"`
}

type TargetScopeField string

const (
	// TargetScopeTopoNode covers host_topo_node and service_topo_node: both
	// are matched against the topology nodes a record belongs to.
	TargetScopeTopoNode TargetScopeField = "TOPO_NODE"
	// TargetScopeHost covers ip / bk_target_ip and dynamic groups of hosts,
	// which Python also reduces to host identities.
	TargetScopeHost TargetScopeField = "HOST"
	// TargetScopeServiceInstance covers service_instance_id.
	TargetScopeServiceInstance TargetScopeField = "SERVICE_INSTANCE"
)

type TargetScopeMethod string

const (
	TargetScopeInclude TargetScopeMethod = "EQ"
	TargetScopeExclude TargetScopeMethod = "NEQ"
)

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
	switch condition.Field {
	case TargetScopeTopoNode, TargetScopeHost, TargetScopeServiceInstance:
	default:
		return fmt.Errorf("alarmd contract: unknown target scope field %q", condition.Field)
	}
	switch condition.Method {
	case TargetScopeInclude, TargetScopeExclude:
	default:
		return fmt.Errorf("alarmd contract: unknown target scope method %q", condition.Method)
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

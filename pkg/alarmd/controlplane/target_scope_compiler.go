// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The strategy item's monitoring target says which hosts, service instances or
// topology nodes the strategy is allowed to alert on. Python reduces it to
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

// compileTargetScope turns a strategy item's target into the frozen scope.
// A nil result means the item names no target, which is the only way a Plan
// may end up without one.
func compileTargetScope(target [][]legacyTargetCondition) (*contract.TargetScopeV2, error) {
	if len(target) == 0 {
		return nil, nil
	}
	stated := false
	for _, group := range target {
		if len(group) > 0 {
			stated = true
			break
		}
	}
	if !stated {
		return nil, nil
	}

	scope := &contract.TargetScopeV2{}
	for _, group := range target {
		compiled := contract.TargetScopeGroupV2{}
		for _, condition := range group {
			field, method, keys, err := compileTargetCondition(condition)
			if err != nil {
				return nil, err
			}
			// Python drops a condition that produced no keys instead of
			// letting it reject everything. Transcribed, not improved.
			if len(keys) == 0 {
				continue
			}
			compiled.Conditions = append(compiled.Conditions, contract.TargetScopeConditionV2{
				Field: field, Method: method, Keys: keys,
			})
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
	method := contract.TargetScopeInclude
	switch strings.ToLower(strings.TrimSpace(condition.Method)) {
	case "eq", "":
		method = contract.TargetScopeInclude
	case "neq":
		method = contract.TargetScopeExclude
	default:
		return "", "", nil, fmt.Errorf("TARGET_SCOPE_UNSUPPORTED: target condition method %q", condition.Method)
	}

	field := strings.ToLower(strings.TrimSpace(condition.Field))
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

	case field == "host_topo_node":
		keys := make([]string, 0, len(values))
		for _, value := range values {
			instance := numberText(value.InstanceID)
			if value.BusinessObjectID == "" || instance == "" {
				continue
			}
			keys = append(keys, value.BusinessObjectID+"|"+instance)
		}
		return contract.TargetScopeTopoNode, method, contract.CanonicalTargetScopeKeys(keys), nil

	case field == "service_topo_node":
		// Matching this needs a record's service-instance topology, which
		// comes from a different CMDB cache than the host one. Until that
		// path exists, admitting the strategy would drop every record whose
		// topology cannot be resolved - false negatives on live alerts, which
		// is worse than leaving the strategy on Python.
		return "", "", nil, fmt.Errorf("TARGET_SCOPE_UNSUPPORTED: service_topo_node target is not resolved yet")

	case field == "dynamic_group":
		// Python resolves a dynamic group against CMDB at match time, so its
		// membership is not a property of the strategy and cannot be frozen
		// with it.
		return "", "", nil, fmt.Errorf("TARGET_SCOPE_UNSUPPORTED: dynamic_group target is resolved per evaluation")

	default:
		return "", "", nil, fmt.Errorf("TARGET_SCOPE_UNSUPPORTED: target field %q", condition.Field)
	}
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
	return "UNSUPPORTED_TARGET_SCOPE"
}

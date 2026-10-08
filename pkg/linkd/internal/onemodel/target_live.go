// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package onemodel

import (
	"context"
	"encoding/json"
)

func isServiceModel(model ModelDefinition) bool {
	return (model.DataSource == "cmdb" || model.DataSource == "legacy") && model.CMDBObjectID == "service_instance"
}

func validateLiveRows(rows []Instance, scope TargetScope, model string, budget *targetBudget) error {
	budget.pages++
	if budget.pages > 256 || len(rows) > 10000 {
		return ErrResultLimit
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.TenantID != scope.TenantID || row.ModelCode != model || !selectionText(row.InstanceID, 1024) || seen[row.InstanceID] {
			return ErrInvalidDataSourceResponse
		}
		seen[row.InstanceID] = true
		ok, err := inBusinessScope(row, scope)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidDataSourceResponse
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return ErrInvalidDataSourceResponse
		}
		budget.bytes += len(raw)
		budget.rows++
		if budget.bytes > 32<<20 || budget.rows > 20000 {
			return ErrResultLimit
		}
	}
	return nil
}

func (r *TargetResolver) topologyBranch(ctx context.Context, scope TargetScope, model ModelDefinition, node string, budget *targetBudget) ([]Instance, bool, error) {
	biz := scope.BusinessIDs[0]
	var rows []Instance
	var present bool
	err := error(ErrTargetUnavailable)
	if r.topology != nil && !isServiceModel(model) {
		var refs []InstanceRef
		refs, present, err = r.topology.Members(ctx, scope.TenantID, biz, model.ModelID, node)
		if err == nil && !present && len(refs) > 0 {
			return nil, false, ErrInvalidDataSourceResponse
		}
		if err == nil && present {
			rows, err = r.explicit(ctx, scope, model, refs, budget)
		}
		if err == nil && len(rows) > 0 {
			return rows, true, nil
		}
	}
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	// 沿用 KAC 的主机缓存空/异常回源；服务实例始终读实际服务来源，不能套用主机 membership。
	if r.live == nil {
		return rows, present, err
	}
	rows, present, err = r.live.TopologyInstances(ctx, scope.TenantID, biz, model, node)
	if err != nil {
		return nil, false, err
	}
	if !present && len(rows) > 0 {
		return nil, false, ErrInvalidDataSourceResponse
	}
	if err := validateLiveRows(rows, scope, model.ModelID, budget); err != nil {
		return nil, false, err
	}
	return rows, present, nil
}

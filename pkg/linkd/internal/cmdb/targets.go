// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

import (
	"context"
	"strconv"
	"time"

	"linkd/internal/onemodel"
)

// ModelDirectory 按租户读取 CMDB 模型映射，不能通过 cw- 前缀猜测节点 canonical ID。
type ModelDirectory interface {
	CMDBModels(context.Context, string) ([]onemodel.ModelDefinition, error)
}

// TargetReader 在模型目录与实时 CMDB 之间适配目标事实；连接仍由调用者管理。
type TargetReader struct {
	*Client
	Directory ModelDirectory
}

// TopologyInstances 使用真实树节点重建 KAC locator，并据成员模型选择主机或服务实例来源。
// 不把 locator 当成实例 ID。服务实例只接受业务、集群或模块节点；其他主线节点明确失败。
func (r TargetReader) TopologyInstances(ctx context.Context, tenant string, biz int64, model onemodel.ModelDefinition, locator string) ([]onemodel.Instance, bool, error) {
	if ctx == nil || r.Client == nil || r.Directory == nil || model.TenantID != tenant || (model.DataSource != "cmdb" && model.DataSource != "legacy") || (model.CMDBObjectID != "host" && model.CMDBObjectID != "service_instance") {
		return nil, false, ErrUnavailable
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	definitions, err := r.Directory.CMDBModels(call, tenant)
	if err != nil {
		return nil, false, err
	}
	if len(definitions) > 1024 {
		return nil, false, ErrIncomplete
	}
	models := map[string]string{}
	for _, definition := range definitions {
		if definition.TenantID != tenant || definition.DataSource != "cmdb" || definition.CMDBObjectID == "" || definition.ModelID == "" || models[definition.CMDBObjectID] != "" {
			return nil, false, ErrIncomplete
		}
		models[definition.CMDBObjectID] = definition.ModelID
	}
	nodes, err := r.Topology(call, tenant, biz)
	if err != nil {
		return nil, false, err
	}
	var selected *Node
	for _, node := range nodes {
		code := models[node.ObjectID]
		if code == "" {
			return nil, false, ErrIncomplete
		}
		// 与 KAC instance_storage_topology._topology_unique_id 相同；不兼容随意拼接的别名。
		if tenant+"_"+code+"_"+strconv.FormatInt(node.InstanceID, 10) == locator {
			if selected != nil {
				return nil, false, ErrIncomplete
			}
			copy := node
			selected = &copy
		}
	}
	if selected == nil {
		return []onemodel.Instance{}, false, nil
	}
	if model.CMDBObjectID == "host" {
		rows, err := r.HostsByTopology(call, tenant, biz, model.ModelID, *selected)
		return rows, err == nil, err
	}
	if selected.ObjectID != "biz" && selected.ObjectID != "set" && selected.ObjectID != "module" {
		return nil, false, ErrUnavailable
	}
	modules := map[int64]bool{}
	parents := map[string]Node{}
	for _, node := range nodes {
		parents[node.ObjectID+":"+strconv.FormatInt(node.InstanceID, 10)] = node
	}
	for _, node := range nodes {
		if node.ObjectID != "module" {
			continue
		}
		ancestor := node
		for depth := 0; depth <= 64; depth++ {
			if ancestor.ObjectID == selected.ObjectID && ancestor.InstanceID == selected.InstanceID {
				modules[node.InstanceID] = true
				break
			}
			if ancestor.ParentObjectID == "" {
				break
			}
			var found bool
			ancestor, found = parents[ancestor.ParentObjectID+":"+strconv.FormatInt(ancestor.ParentInstanceID, 10)]
			if !found {
				return nil, false, ErrIncomplete
			}
		}
	}
	rows, err := r.ServiceInstances(call, tenant, biz, model.ModelID, nil)
	if err != nil {
		return nil, false, err
	}
	result := []onemodel.Instance{}
	for _, row := range rows {
		module, ok := row.Attributes["bk_module_id"].(int64)
		if !ok {
			return nil, false, ErrIncomplete
		}
		if selected.ObjectID == "biz" || modules[module] {
			result = append(result, row)
		}
	}
	return result, true, call.Err()
}

var _ onemodel.LiveTargetReader = TargetReader{}

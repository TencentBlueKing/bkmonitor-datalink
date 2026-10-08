// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

import (
	"context"
	"maps"
	"strconv"
	"time"

	"linkd/internal/onemodel"
)

// Node 是业务主线中的实际节点；对象 ID 必须再通过租户模型目录转换，不能猜测 canonical 模型。
type Node struct {
	ObjectID         string // ObjectID 是 CMDB bk_obj_id。
	InstanceID       int64  // InstanceID 是当前模型内的正整数身份。
	ParentObjectID   string // ParentObjectID 为空仅用于业务根。
	ParentInstanceID int64  // ParentInstanceID 与父对象一起定位父节点。
}

// Topology 读取完整业务树并补充内置空闲模块；重复节点、错误根及过深/过大树均拒绝。
// 返回值不包含跨业务节点，空闲模块沿用 KAC get_biz_internal_module 的合并规则。
func (c *Client) Topology(ctx context.Context, tenant string, biz int64) ([]Node, error) {
	if ctx == nil {
		return nil, ErrUnavailable
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var roots []rawNode
	if err := c.request(call, tenant, biz, "search_biz_inst_topo", nil, &roots); err != nil {
		return nil, err
	}
	if len(roots) != 1 || roots[0].ObjectID != "biz" || roots[0].InstanceID != biz {
		return nil, ErrIncomplete
	}
	var internal struct {
		SetID   *int64 `json:"bk_set_id"`
		Modules []struct {
			ID int64 `json:"bk_module_id"`
		} `json:"module"`
	}
	if err := c.request(call, tenant, biz, "get_biz_internal_module", nil, &internal); err != nil {
		return nil, err
	}
	nodes := []Node{}
	seen := map[string]bool{}
	var walk func(rawNode, string, int64, int) error
	walk = func(raw rawNode, parent string, parentID int64, depth int) error {
		if depth > 64 || len(nodes) >= 10000 || raw.ObjectID == "" || len(raw.ObjectID) > 128 || raw.InstanceID < 1 || raw.ObjectID == "biz" && (parent != "" || raw.InstanceID != biz) {
			return ErrIncomplete
		}
		if raw.Tenant != "" && raw.Tenant != tenant || raw.Business != nil && *raw.Business != biz {
			return ErrIncomplete
		}
		key := raw.ObjectID + ":" + strconv.FormatInt(raw.InstanceID, 10)
		if seen[key] {
			return ErrIncomplete
		}
		seen[key] = true
		nodes = append(nodes, Node{ObjectID: raw.ObjectID, InstanceID: raw.InstanceID, ParentObjectID: parent, ParentInstanceID: parentID})
		for _, child := range raw.Children {
			if err := walk(child, raw.ObjectID, raw.InstanceID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(roots[0], "", 0, 0); err != nil {
		return nil, err
	}
	if internal.SetID != nil {
		if *internal.SetID < 1 || internal.Modules == nil {
			return nil, ErrIncomplete
		}
		if !seen["set:"+strconv.FormatInt(*internal.SetID, 10)] {
			node := rawNode{ObjectID: "set", InstanceID: *internal.SetID}
			for _, module := range internal.Modules {
				node.Children = append(node.Children, rawNode{ObjectID: "module", InstanceID: module.ID})
			}
			if err := walk(node, "biz", biz, 1); err != nil {
				return nil, err
			}
		}
	} else if len(internal.Modules) > 0 {
		return nil, ErrIncomplete
	}
	return nodes, call.Err()
}

type rawNode struct {
	ObjectID   string    `json:"bk_obj_id"`
	InstanceID int64     `json:"bk_inst_id"`
	Tenant     string    `json:"bk_tenant_id"`
	Business   *int64    `json:"bk_biz_id"`
	Children   []rawNode `json:"child"`
}

// HostsByTopology 在已校验的业务节点读取全部主机，并直接构造事实，不依赖失效的 ES 实例缓存。
// 调用者必须先通过 Topology 确认节点存在和业务归属；完整空结果才表示零成员。
func (c *Client) HostsByTopology(ctx context.Context, tenant string, biz int64, model string, node Node) ([]onemodel.Instance, error) {
	if ctx == nil || model == "" || node.ObjectID == "" || node.InstanceID < 1 {
		return nil, ErrUnavailable
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := c.pages(call, tenant, biz, "find_host_by_topo", map[string]any{"bk_obj_id": node.ObjectID, "bk_inst_id": node.InstanceID, "fields": []string{"bk_host_id", "bk_host_innerip", "bk_cloud_id", "bk_host_name"}}, "bk_host_id", &readBudget{})
	if err != nil {
		return nil, err
	}
	result := make([]onemodel.Instance, 0, len(rows))
	for _, row := range rows {
		id, _ := positive(row["bk_host_id"])
		ip, ok := "", true
		if row["bk_host_innerip"] != nil {
			ip, ok = row["bk_host_innerip"].(string)
		}
		if !ok {
			return nil, ErrIncomplete
		}
		name := ""
		if v := row["bk_host_name"]; v != nil {
			name, ok = v.(string)
			if !ok {
				return nil, ErrIncomplete
			}
		}
		instanceID := strconv.FormatInt(id, 10)
		attributes := maps.Clone(row)
		if _, exists := attributes["ip"]; !exists {
			attributes["ip"] = ip
		}
		display := ip
		if display == "" {
			display = name
		}
		result = append(result, onemodel.Instance{TenantID: tenant, ModelCode: model, InstanceID: instanceID, Fields: map[string]any{"model_inst_id": instanceID, "entity_uid": model + "|" + instanceID, "display_name": display, "bk_biz_ids": []int64{biz}, "source": "cmdb_direct"}, Attributes: attributes})
	}
	return result, call.Err()
}

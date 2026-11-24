// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

// ServiceInstances 返回具体业务内的权威服务实例及执行主机属性。
// ids=nil 读取全量，空切片返回空结果；非空 ids 要求全部存在。缺主机、缺页、数量变化均失败。
// 十秒覆盖整个批次；分页最多 256 页、20000 条及 32 MiB，不能把截断数据用于屏蔽解除。
func (c *Client) ServiceInstances(ctx context.Context, tenant string, biz int64, model string, ids []string) ([]onemodel.Instance, error) {
	if ctx == nil || biz < 1 || model == "" || len(ids) > 10000 {
		return nil, ErrUnavailable
	}
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil {
		return nil, ErrUnavailable
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if ids != nil && len(ids) == 0 {
		return []onemodel.Instance{}, call.Err()
	}
	budget := &readBudget{}
	var details []map[string]any
	if ids == nil {
		rows, err := c.pages(call, tenant, biz, "list_service_instance_detail", nil, "id", budget)
		if err != nil {
			return nil, err
		}
		details = rows
	} else {
		wanted := slices.Clone(ids)
		slices.Sort(wanted)
		wanted = slices.Compact(wanted)
		for start := 0; start < len(wanted); start += 200 {
			part := wanted[start:min(start+200, len(wanted))]
			numbers := []int64{}
			for _, id := range part {
				n, err := strconv.ParseInt(id, 10, 64)
				if err != nil || n < 1 || strconv.FormatInt(n, 10) != id {
					return nil, ErrIncomplete
				}
				numbers = append(numbers, n)
			}
			rows, err := c.pages(call, tenant, biz, "list_service_instance_detail", map[string]any{"service_instance_ids": numbers}, "id", budget)
			if err != nil {
				return nil, err
			}
			if len(rows) != len(part) {
				return nil, ErrIncomplete
			}
			for _, row := range rows {
				n, ok := positive(row["id"])
				if !ok || !slices.Contains(part, strconv.FormatInt(n, 10)) {
					return nil, ErrIncomplete
				}
			}
			details = append(details, rows...)
		}
	}
	hostIDs := []int64{}
	for _, row := range details {
		b, ok := positive(row["bk_biz_id"])
		host, hostOK := positive(row["bk_host_id"])
		_, moduleOK := positive(row["bk_module_id"])
		if !ok || b != biz || !hostOK || !moduleOK {
			return nil, ErrIncomplete
		}
		hostIDs = append(hostIDs, host)
	}
	slices.Sort(hostIDs)
	hostIDs = slices.Compact(hostIDs)
	hosts := map[int64]map[string]any{}
	for start := 0; start < len(hostIDs); start += 200 {
		part := hostIDs[start:min(start+200, len(hostIDs))]
		body := map[string]any{"fields": []string{"bk_host_id", "bk_host_innerip", "bk_cloud_id", "bk_host_name"}, "host_property_filter": map[string]any{"condition": "AND", "rules": []any{map[string]any{"field": "bk_host_id", "operator": "in", "value": part}}}}
		rows, err := c.pages(call, tenant, biz, "list_biz_hosts", body, "bk_host_id", budget)
		if err != nil {
			return nil, err
		}
		if len(rows) != len(part) {
			return nil, ErrIncomplete
		}
		for _, host := range rows {
			id, ok := positive(host["bk_host_id"])
			if !ok || !slices.Contains(part, id) {
				return nil, ErrIncomplete
			}
			hosts[id] = host
		}
	}
	result := make([]onemodel.Instance, 0, len(details))
	for _, row := range details {
		id, _ := positive(row["id"])
		hostID, _ := positive(row["bk_host_id"])
		moduleID, _ := positive(row["bk_module_id"])
		host := hosts[hostID]
		cloudID, ok := int64(0), true
		if host["bk_cloud_id"] != nil {
			cloudID, ok = nonnegative(host["bk_cloud_id"])
		}
		if !ok {
			return nil, ErrIncomplete
		}
		name, nameOK := "", true
		if row["name"] != nil {
			name, nameOK = row["name"].(string)
		}
		ip, ipOK := "", true
		if host["bk_host_innerip"] != nil {
			ip, ipOK = host["bk_host_innerip"].(string)
		}
		if !nameOK || !ipOK {
			return nil, ErrIncomplete
		}
		templateID := int64(0)
		if value := row["service_template_id"]; value != nil {
			templateID, ok = nonnegative(value)
			if !ok {
				return nil, ErrIncomplete
			}
		}
		attributes := map[string]any{"service_instance_id": id, "bk_module_id": moduleID, "service_template_id": templateID, "bk_host_id": hostID, "bk_cloud_id": cloudID, "bk_host_innerip": ip, "process_instances": row["process_instances"]}
		result = append(result, onemodel.Instance{TenantID: tenant, ModelCode: model, InstanceID: strconv.FormatInt(id, 10), Attributes: attributes, Fields: map[string]any{"model_inst_id": strconv.FormatInt(id, 10), "entity_uid": model + "|" + strconv.FormatInt(id, 10), "display_name": name, "bk_biz_ids": []int64{biz}, "source": "cmdb_service_instance_direct"}})
	}
	slices.SortFunc(result, func(a, b onemodel.Instance) int { return strings.Compare(a.InstanceID, b.InstanceID) })
	return result, call.Err()
}

type readBudget struct{ pages, rows, bytes int }

func (c *Client) pages(ctx context.Context, tenant string, biz int64, action string, params map[string]any, idKey string, budget *readBudget) ([]map[string]any, error) {
	rows := []map[string]any{}
	seen := map[int64]bool{}
	expected := int64(-1)
	for {
		budget.pages++
		if budget.pages > 256 {
			return nil, ErrIncomplete
		}
		body := maps.Clone(params)
		if body == nil {
			body = map[string]any{}
		}
		body["page"] = map[string]any{"start": len(rows), "limit": 200}
		var data struct {
			Count *int64           `json:"count"`
			Info  []map[string]any `json:"info"`
		}
		if err := c.request(ctx, tenant, biz, action, body, &data); err != nil {
			return nil, err
		}
		if data.Count == nil || *data.Count < 0 || *data.Count > 20000 || data.Info == nil || len(data.Info) > 200 {
			return nil, ErrIncomplete
		}
		if expected < 0 {
			expected = *data.Count
		}
		if *data.Count != expected || int64(len(rows)+len(data.Info)) > expected || len(data.Info) == 0 && int64(len(rows)) < expected {
			return nil, ErrIncomplete
		}
		for _, row := range data.Info {
			id, ok := positive(row[idKey])
			if !ok || seen[id] {
				return nil, ErrIncomplete
			}
			seen[id] = true
			if responseTenant, exists := row["bk_tenant_id"]; exists && responseTenant != tenant {
				return nil, ErrIncomplete
			}
			if responseBiz, exists := row["bk_biz_id"]; exists {
				b, ok := positive(responseBiz)
				if !ok || b != biz {
					return nil, ErrIncomplete
				}
			}
			raw, err := json.Marshal(row)
			if err != nil {
				return nil, ErrIncomplete
			}
			budget.rows++
			budget.bytes += len(raw)
			if budget.rows > 20000 || budget.bytes > 32<<20 {
				return nil, ErrIncomplete
			}
			rows = append(rows, row)
		}
		if int64(len(rows)) == expected {
			return rows, nil
		}
	}
}

func positive(v any) (int64, bool) { n, ok := nonnegative(v); return n, ok && n > 0 }

func nonnegative(v any) (int64, bool) {
	// API JSON 的整型字段必须是数字；不接受 bool、浮点数或字符串形式的身份。
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	id, err := n.Int64()
	return id, err == nil && id >= 0
}

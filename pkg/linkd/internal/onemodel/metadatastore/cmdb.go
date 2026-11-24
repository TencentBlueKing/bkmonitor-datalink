// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metadatastore

import (
	"context"

	"linkd/internal/onemodel"
)

// CMDBModels 读取 KAC 主线拓扑使用的租户 CMDB 模型映射，不创建缺失模型、不按命名前缀猜测。
// 与 KAC 节点投影生产者一致，仅 datasource=cmdb；相同 CMDB 对象存在多个映射时不能唯一定位。
func (d *Directory) CMDBModels(ctx context.Context, tenant string) ([]onemodel.ModelDefinition, error) {
	call, cancel, err := queryScope(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	rows, err := d.db.QueryContext(call, "SELECT bk_tenant_id, model_id, datasource, bk_cmdb_obj_id FROM object_model_v2 WHERE bk_tenant_id=? AND datasource='cmdb' AND bk_cmdb_obj_id<>'' ORDER BY model_id LIMIT 1025", tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []onemodel.ModelDefinition{}
	seen := map[string]bool{}
	for rows.Next() {
		var model onemodel.ModelDefinition
		if err := rows.Scan(&model.TenantID, &model.ModelID, &model.DataSource, &model.CMDBObjectID); err != nil {
			return nil, err
		}
		if model.TenantID != tenant || model.DataSource != "cmdb" || model.ModelID == "" || len(model.ModelID) > 128 || model.CMDBObjectID == "" || len(model.CMDBObjectID) > 128 || seen[model.CMDBObjectID] {
			return nil, onemodel.ErrInvalidDataSourceResponse
		}
		seen[model.CMDBObjectID] = true
		result = append(result, model)
		if len(result) > 1024 {
			return nil, onemodel.ErrResultLimit
		}
	}
	return result, rows.Err()
}

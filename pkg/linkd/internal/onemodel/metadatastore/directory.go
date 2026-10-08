// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package metadatastore 只读 Kingeye 的业务空间、对象模型和动态分组定义，不读取成员快照或迁移其表。
package metadatastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

// Directory 持有调用者管理的共享只读连接，不创建或关闭数据库。
type Directory struct{ db *sql.DB }

// New 创建目标定义 Reader；连接的可达性只在实际查询时检查。
func New(db *sql.DB) (*Directory, error) {
	if db == nil {
		return nil, fmt.Errorf("target definition database required")
	}
	return &Directory{db: db}, nil
}

func queryScope(ctx context.Context, tenant string) (context.Context, context.CancelFunc, error) {
	if err := domain.ValidateIdentityPart("bk_tenant_id", tenant, 64); err != nil {
		return nil, nil, err
	}
	if ctx == nil {
		return nil, nil, fmt.Errorf("context required")
	}
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	return call, cancel, nil
}

// Model 读取租户内的模型和当前字段目录；缺失、重复、非法字段目录均不扩大查询范围。
func (d *Directory) Model(ctx context.Context, tenant, model string) (onemodel.ModelDefinition, bool, error) {
	if model == "" || len(model) > 128 {
		return onemodel.ModelDefinition{}, false, onemodel.ErrInvalidQuery
	}
	call, cancel, err := queryScope(ctx, tenant)
	if err != nil {
		return onemodel.ModelDefinition{}, false, err
	}
	defer cancel()
	rows, err := d.db.QueryContext(call, "SELECT bk_tenant_id, model_id, datasource, bk_cmdb_obj_id, attribute_config FROM object_model_v2 WHERE bk_tenant_id=? AND model_id=? LIMIT 2", tenant, model)
	if err != nil {
		return onemodel.ModelDefinition{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return onemodel.ModelDefinition{}, false, rows.Err()
	}
	var result onemodel.ModelDefinition
	var raw []byte
	if err := rows.Scan(&result.TenantID, &result.ModelID, &result.DataSource, &result.CMDBObjectID, &raw); err != nil {
		return onemodel.ModelDefinition{}, false, err
	}
	if rows.Next() || result.TenantID != tenant || result.ModelID != model {
		return onemodel.ModelDefinition{}, false, onemodel.ErrInvalidDataSourceResponse
	}
	if err := rows.Err(); err != nil {
		return onemodel.ModelDefinition{}, false, err
	}
	result.AttributeTypes, err = attributeTypes(raw)
	if err != nil {
		return onemodel.ModelDefinition{}, false, err
	}
	return result, true, nil
}

func attributeTypes(raw []byte) (map[string]onemodel.InstanceAttributeType, error) {
	if len(raw) > 1<<20 {
		return nil, onemodel.ErrResultLimit
	}
	normalized, err := (domain.JSONObject{"attributes": raw}).Normalize()
	if err != nil {
		return nil, onemodel.ErrInvalidDataSourceResponse
	}
	var config struct {
		Config []struct {
			ID   string `json:"bk_property_id"`
			Type string `json:"bk_property_type"`
		} `json:"config"`
	}
	if err := json.Unmarshal(normalized["attributes"], &config); err != nil {
		return nil, onemodel.ErrInvalidDataSourceResponse
	}
	if len(config.Config) > 1024 {
		return nil, onemodel.ErrResultLimit
	}
	result := map[string]onemodel.InstanceAttributeType{}
	for _, field := range config.Config {
		if field.ID == "" || len(field.ID) > 128 {
			return nil, onemodel.ErrInvalidDataSourceResponse
		}
		if _, ok := result[field.ID]; ok {
			return nil, onemodel.ErrInvalidDataSourceResponse
		}
		result[field.ID] = onemodel.NormalizeAttributeType(field.Type)
	}
	return result, nil
}

// Space 按真实租户业务主键读取全局属性，不将固定 ID 当成全局标记。
func (d *Directory) Space(ctx context.Context, tenant string, biz int64) (onemodel.BusinessSpace, bool, error) {
	if biz < 1 {
		return onemodel.BusinessSpace{}, false, onemodel.ErrInvalidQuery
	}
	call, cancel, err := queryScope(ctx, tenant)
	if err != nil {
		return onemodel.BusinessSpace{}, false, err
	}
	defer cancel()
	id := strconv.FormatInt(biz, 10)
	rows, err := d.db.QueryContext(call, "SELECT bk_tenant_id, space_id, is_global FROM metadata_space WHERE bk_tenant_id=? AND space_type_id=? AND space_id=? LIMIT 2", tenant, "bkcc", id)
	if err != nil {
		return onemodel.BusinessSpace{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return onemodel.BusinessSpace{}, false, rows.Err()
	}
	var result onemodel.BusinessSpace
	var saved string
	if err := rows.Scan(&result.TenantID, &saved, &result.Global); err != nil {
		return onemodel.BusinessSpace{}, false, err
	}
	if rows.Next() || result.TenantID != tenant || saved != id {
		return onemodel.BusinessSpace{}, false, onemodel.ErrInvalidDataSourceResponse
	}
	result.BusinessID = biz
	return result, true, rows.Err()
}

// DynamicGroup 每次按主键读取当前条件和范围；不访问 dynamic_group_member_v2。
func (d *Directory) DynamicGroup(ctx context.Context, tenant, id string) (onemodel.DynamicGroupDefinition, bool, error) {
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil || number < 1 || strconv.FormatInt(number, 10) != id {
		return onemodel.DynamicGroupDefinition{}, false, onemodel.ErrInvalidQuery
	}
	call, cancel, err := queryScope(ctx, tenant)
	if err != nil {
		return onemodel.DynamicGroupDefinition{}, false, err
	}
	defer cancel()
	rows, err := d.db.QueryContext(call, "SELECT bk_tenant_id, dynamic_group_id, object_model_code, space_code, condition_list FROM dynamic_group_v2 WHERE bk_tenant_id=? AND dynamic_group_id=? LIMIT 2", tenant, number)
	if err != nil {
		return onemodel.DynamicGroupDefinition{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return onemodel.DynamicGroupDefinition{}, false, rows.Err()
	}
	var result onemodel.DynamicGroupDefinition
	var saved int64
	var raw []byte
	if err := rows.Scan(&result.TenantID, &saved, &result.ModelID, &result.SpaceCode, &raw); err != nil {
		return onemodel.DynamicGroupDefinition{}, false, err
	}
	if rows.Next() || result.TenantID != tenant || saved != number || result.ModelID == "" {
		return onemodel.DynamicGroupDefinition{}, false, onemodel.ErrInvalidDataSourceResponse
	}
	if err := rows.Err(); err != nil {
		return onemodel.DynamicGroupDefinition{}, false, err
	}
	if len(raw) > 128<<10 {
		return onemodel.DynamicGroupDefinition{}, false, onemodel.ErrResultLimit
	}
	normalized, err := (domain.JSONObject{"conditions": raw}).Normalize()
	if err != nil {
		return onemodel.DynamicGroupDefinition{}, false, errors.Join(onemodel.ErrInvalidDataSourceResponse, err)
	}
	result.ID = id
	result.Conditions = normalized["conditions"]
	return result, true, nil
}

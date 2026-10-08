// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

// DescriptionConfigurationClient 只读取 SplitRecord 的同版本发布材料，不校验旧 Set/Config 表。
// 当前态没有历史保留；版本不匹配或覆盖缺少独立修订时明确失败。
type DescriptionConfigurationClient struct{ db *gorm.DB }

var _ description.ConfigurationReader = (*DescriptionConfigurationClient)(nil)

// NewDescriptionConfigurationClient 注入只读连接，不迁移 schema。
func NewDescriptionConfigurationClient(db *gorm.DB) (*DescriptionConfigurationClient, error) {
	if db == nil {
		return nil, fmt.Errorf("description configuration requires database")
	}
	return &DescriptionConfigurationClient{db: db}, nil
}

// ReadConfiguration 用单条有界 SELECT 固定版本、身份和渲染材料；所有策略内容
// 来自这一行，不把后来编辑的 Set/Config 混入，也不以当前版本替代触发时版本。
func (c *DescriptionConfigurationClient) ReadConfiguration(ctx context.Context, query description.ConfigurationQuery) (description.Configuration, error) {
	if ctx == nil || !query.Valid() {
		return description.Configuration{}, descriptionFailure("configuration_identity_invalid")
	}
	if err := ctx.Err(); err != nil {
		return description.Configuration{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, strategyReadTimeout)
	defer cancel()
	row, found, err := readStrategyPublication(ctx, c.db, models.StrategyQuery{TenantID: query.TenantID, ID: query.StrategyID, Version: query.StrategyVersion})
	if err != nil {
		return description.Configuration{}, err
	}
	if !found {
		return description.Configuration{}, descriptionFailure("publication_missing_or_ambiguous")
	}
	if row.ParentID != nil {
		// 覆盖编辑复用父记录版本，整数标签不能证明触发时覆盖内容，不能用当前覆盖冒充。
		return description.Configuration{}, descriptionFailure("publication_override_revision_missing")
	}
	payload, err := decodeStrategyPublication(row)
	if err != nil {
		return description.Configuration{}, err
	}
	if len(payload.Runtime) != len(payload.Resolved) {
		return description.Configuration{}, descriptionFailure("publication_runtime_invalid")
	}
	strategy, err := cwStrategyFromPublication(row, payload)
	if err != nil {
		return description.Configuration{}, err
	}
	var config models.StrategySetConfig
	if json.Unmarshal(payload.Config, &config) != nil || !sameStrategyUUID(config.ID, row.ConfigUID) {
		return description.Configuration{}, descriptionFailure("publication_binding_invalid")
	}
	if !config.Enable {
		return description.Configuration{}, descriptionFailure("configuration_disabled_or_invalid")
	}
	runtime := payload.Runtime[0]
	if len(runtime.Error) > 0 || len(runtime.Queries) == 0 || len(runtime.Queries) > 32 || len(runtime.Algorithms) > 32 {
		return description.Configuration{}, descriptionFailure("publication_runtime_invalid")
	}
	return description.Configuration{Identity: query, Spec: strategy.Spec, SetConfig: config, Queries: runtime.Queries, Algorithms: runtime.Algorithms}, nil
}

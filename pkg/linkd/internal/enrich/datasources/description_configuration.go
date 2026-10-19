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

// DescriptionConfigurationClient 只读取同一不可变 Strategy revision 的声明与执行事实。
type DescriptionConfigurationClient struct{ db *gorm.DB }

var _ description.ConfigurationReader = (*DescriptionConfigurationClient)(nil)

// NewDescriptionConfigurationClient 注入只读连接，不迁移 schema。
func NewDescriptionConfigurationClient(db *gorm.DB) (*DescriptionConfigurationClient, error) {
	if db == nil {
		return nil, fmt.Errorf("description configuration requires database")
	}
	return &DescriptionConfigurationClient{db: db}, nil
}

// ReadConfiguration 用单条有界 SELECT 读取精确历史快照；声明 metadata/spec 与执行
// items 必须来自同一行，不依赖 SplitRecord 的当前内容或 publish_status。
func (c *DescriptionConfigurationClient) ReadConfiguration(ctx context.Context, query description.ConfigurationQuery) (description.Configuration, error) {
	if ctx == nil || !query.Valid() {
		return description.Configuration{}, descriptionFailure("configuration_identity_invalid")
	}
	if err := ctx.Err(); err != nil {
		return description.Configuration{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, strategyReadTimeout)
	defer cancel()
	row, found, err := readStrategyRevision(ctx, c.db, models.StrategyQuery{TenantID: query.TenantID, ID: query.StrategyID, Version: query.StrategyVersion})
	if err != nil {
		return description.Configuration{}, err
	}
	if !found {
		return description.Configuration{}, descriptionFailure("strategy_revision_missing")
	}
	snapshot, err := decodeStrategyRevision(row)
	if err != nil {
		return description.Configuration{}, err
	}
	strategy, err := cwStrategyFromRevision(row, snapshot)
	if err != nil {
		return description.Configuration{}, err
	}
	// 当前配置的启停不影响历史；仅检查该快照自身是否能产生本次触发。
	if (snapshot.Strategy.Enabled != nil && !*snapshot.Strategy.Enabled) || snapshot.Strategy.Invalid || snapshot.Strategy.Deleted || (strategy.Spec.Enable != nil && !*strategy.Spec.Enable) {
		return description.Configuration{}, descriptionFailure("configuration_disabled_or_invalid")
	}
	// 标准 Event 尚无可信 item 身份。多项快照不能按数组顺序选第一项。
	if len(snapshot.Strategy.Items) != 1 {
		return description.Configuration{}, descriptionFailure("strategy_revision_items_ambiguous")
	}
	var runtime struct {
		Error json.RawMessage `json:"error"`
		// 内容只依赖单位和指标身份；运行时的其他字段不服从声明查询 DTO 的类型。
		Queries []struct {
			Unit     string `json:"unit"`
			MetricID string `json:"metric_id"`
		} `json:"query_configs"`
		Algorithms []description.RuntimeAlgorithm `json:"algorithms"`
	}
	if json.Unmarshal(snapshot.Strategy.Items[0], &runtime) != nil {
		return description.Configuration{}, descriptionFailure("strategy_revision_runtime_invalid")
	}
	if len(runtime.Error) > 0 || len(runtime.Queries) == 0 || len(runtime.Queries) > 32 || len(runtime.Algorithms) > 32 {
		return description.Configuration{}, descriptionFailure("strategy_revision_runtime_invalid")
	}
	queries := make([]models.StrategyQueryConfig, len(runtime.Queries))
	for i, query := range runtime.Queries {
		queries[i] = models.StrategyQueryConfig{Unit: query.Unit, MetricID: query.MetricID}
	}
	return description.Configuration{Identity: query, Spec: strategy.Spec, Queries: queries, Algorithms: runtime.Algorithms}, nil
}

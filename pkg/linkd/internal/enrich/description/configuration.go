// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package description

import (
	"context"
	"encoding/json"
	"strings"

	"linkd/internal/enrich/models"
)

// ConfigurationQuery 使用触发事件的身份读取发布时配置，不按当前时间或版本大小猜测。
type ConfigurationQuery struct {
	TenantID        string
	StrategyID      int64
	StrategyVersion int64
	BusinessID      int64
}

// Valid 判断读取身份是否完整；正整数版本包括拆分发布的长整数。
func (q ConfigurationQuery) Valid() bool {
	return q.TenantID != "" && strings.TrimSpace(q.TenantID) == q.TenantID && q.StrategyID > 0 && q.StrategyVersion > 0 && q.BusinessID != 0
}

// RuntimeAlgorithm 是该版本发布时已经编译的检测参数，顺序与监控项中的算法顺序一致。
type RuntimeAlgorithm struct {
	Type       string          `json:"type"`
	Level      int64           `json:"level"`
	UnitPrefix string          `json:"unit_prefix"`
	Config     json.RawMessage `json:"config"`
}

// Configuration 保存同一次版本绑定读取冻结的内容依赖。
// Spec 与 SetConfig 来自当前 Set/Config，Reader 必须证明它们的渲染依赖
// 与指定发布版本一致；Queries/Algorithms 保留该版本实际下发的单位和算法参数。
type Configuration struct {
	Identity   ConfigurationQuery
	Spec       models.CWStrategySpec
	SetConfig  models.StrategySetConfig
	Queries    []models.StrategyQueryConfig
	Algorithms []RuntimeAlgorithm
}

// ConfigurationReader 原子读取并校验指定版本的完整配置身份。
// 缺失、过期或歧义返回永久内容错误；连接、超时等依赖错误保留错误链以便重试。
type ConfigurationReader interface {
	ReadConfiguration(context.Context, ConfigurationQuery) (Configuration, error)
}

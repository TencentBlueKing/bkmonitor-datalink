// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// TimeGraphResourceConfig 保存内存图中识别和呈现资源节点所需的字段。
type TimeGraphResourceConfig struct {
	Name  cmdb.Resource
	Index cmdb.Index
	Info  cmdb.Index
}

// TimeGraphRelationConfig 描述生成一条图边所需的指标和关系身份。
type TimeGraphRelationConfig struct {
	Resources     []cmdb.Resource
	RelationType  string
	MetricName    string
	Category      string
	IsDirectional bool
}

// TimeGraphConfig 是 TimeGraph 使用的命名空间级配置快照。
// 单次查询内保持不变，避免并发 schema 重载在查询中途改变节点身份规则。
type TimeGraphConfig struct {
	Resource []TimeGraphResourceConfig
	Relation []TimeGraphRelationConfig
}

func (m *Model) timeGraphConfig(namespace string) *TimeGraphConfig {
	return buildTimeGraphConfig(m.getSchemaProvider(), namespace)
}

func buildTimeGraphConfig(provider SchemaProvider, namespace string) *TimeGraphConfig {
	config := &TimeGraphConfig{}

	for _, resourceType := range provider.ListResourceTypes(namespace) {
		primaryKeys := provider.GetResourcePrimaryKeys(namespace, resourceType)
		fields := provider.GetResourceFields(namespace, resourceType)
		primarySet := make(map[string]struct{}, len(primaryKeys))
		for _, field := range primaryKeys {
			primarySet[field] = struct{}{}
		}
		info := make(cmdb.Index, 0, len(fields))
		for _, field := range fields {
			if _, ok := primarySet[field]; !ok {
				info = append(info, field)
			}
		}
		config.Resource = append(config.Resource, TimeGraphResourceConfig{
			Name:  cmdb.Resource(resourceType),
			Index: append(cmdb.Index(nil), primaryKeys...),
			Info:  info,
		})
	}

	for _, schema := range provider.ListRelationSchemas(namespace) {
		config.Relation = append(config.Relation, TimeGraphRelationConfig{
			Resources: []cmdb.Resource{
				cmdb.Resource(schema.FromType),
				cmdb.Resource(schema.ToType),
			},
			RelationType:  string(schema.RelationType),
			MetricName:    schema.MetricName,
			Category:      string(schema.Category),
			IsDirectional: schema.IsDirectional,
		})
	}

	return config
}

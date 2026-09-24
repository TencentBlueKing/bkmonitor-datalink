// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// TimeGraphResourceConfig contains the fields needed to identify and render a
// resource node in the in-memory graph.
type TimeGraphResourceConfig struct {
	Name  cmdb.Resource
	Index cmdb.Index
	Info  cmdb.Index
}

// TimeGraphRelationConfig describes the metric and relation identity used to
// materialize one graph edge.
type TimeGraphRelationConfig struct {
	Resources     []cmdb.Resource
	RelationType  string
	MetricName    string
	Category      string
	IsDirectional bool
}

// TimeGraphConfig is the namespace-scoped snapshot consumed by TimeGraph.
// Keeping this snapshot immutable for the duration of one query prevents a
// concurrent schema reload from changing node identity rules mid-query.
type TimeGraphConfig struct {
	Resource     []TimeGraphResourceConfig
	Relation     []TimeGraphRelationConfig
	MaxNodes     int
	MaxEdges     int
	MaxResults   int
	MaxNodeInfos int
}

func (m *Model) timeGraphConfig(namespace string) *TimeGraphConfig {
	return buildTimeGraphConfig(m.getSchemaProvider(), namespace)
}

func buildTimeGraphConfig(provider SchemaProvider, namespace string) *TimeGraphConfig {
	config := &TimeGraphConfig{
		MaxNodes:     effectiveMaxGraphNodes(),
		MaxEdges:     effectiveMaxGraphEdges(),
		MaxResults:   effectiveMaxGraphResults(),
		MaxNodeInfos: effectiveMaxGraphNodeInfos(),
	}

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

func defaultTimeGraphConfig() *TimeGraphConfig {
	return buildTimeGraphConfig(GetSchemaProvider(), "")
}

// The helpers below are kept for callers that construct a TimeGraph without a
// namespace-specific snapshot. Serving queries always pass a snapshot from
// timeGraphConfig; these fallbacks use the static v1beta3 schema.
func ResourcesIndex(resources ...cmdb.Resource) cmdb.Index {
	config := defaultTimeGraphConfig()
	byResource := make(map[cmdb.Resource]TimeGraphResourceConfig, len(config.Resource))
	for _, resource := range config.Resource {
		byResource[resource.Name] = resource
	}
	var result cmdb.Index
	for _, resource := range resources {
		result = append(result, byResource[resource].Index...)
	}
	return result
}

func ResourcesInfo(resource cmdb.Resource) cmdb.Index {
	config := defaultTimeGraphConfig()
	for _, item := range config.Resource {
		if item.Name == resource {
			return append(cmdb.Index(nil), item.Info...)
		}
	}
	return nil
}

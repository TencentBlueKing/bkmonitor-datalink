// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 蓝鲸监控 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

var _ enrich.K8sReader = (*OneModelK8sReader)(nil)

// OneModelK8sReader 将 K8s 维度映射为统一实例查询。
// 它复用 OneModelReader 的 kingeye_all_instance 查询和租户/身份复核，不直接读取 kmc_k8s_* 索引。
type OneModelK8sReader struct {
	instances enrich.OneModelReader
}

// NewOneModelK8sReader 创建基于 OneModelReader 的 K8s Reader。
func NewOneModelK8sReader(instances enrich.OneModelReader) (*OneModelK8sReader, error) {
	if instances == nil {
		return nil, fmt.Errorf("create onemodel k8s reader: one model reader is required")
	}
	return &OneModelK8sReader{instances: instances}, nil
}

// FindK8sInstance 按 K8s 对象模型和旧 KAC 身份字段查询统一实例。
func (r *OneModelK8sReader) FindK8sInstance(ctx context.Context, tenantID, modelCode string, dimensions domain.DimensionMap) (enrich.Instance, bool, error) {
	filters, err := k8sInstanceFilters(modelCode, dimensions)
	if err != nil {
		return enrich.Instance{}, false, err
	}
	return r.instances.FindInstance(ctx, tenantID, enrich.InstanceQuery{ModelCode: modelCode, AttributeFilters: filters})
}

func k8sInstanceFilters(modelCode string, dimensions domain.DimensionMap) ([]enrich.InstanceAttributeFilter, error) {
	fields := map[string][]string{
		"cw-K8s_Cluster":   {"cluster_id"},
		"cw-K8s_Namespace": {"cluster_id", "namespace"},
		"cw-K8s_Service":   {"cluster_id", "namespace", "name"},
		"cw-K8s_Workload":  {"cluster_id", "namespace", "type", "name"},
		"cw-K8s_Pod":       {"cluster_id", "namespace", "name"},
		"cw-K8s_Container": {"cluster_id", "namespace", "pod_name", "name"},
		"cw-K8s_Node":      {"cluster_id", "name"},
	}[modelCode]
	if fields == nil {
		return nil, fmt.Errorf("unsupported k8s model code %q", modelCode)
	}
	values := map[string]string{
		"cluster_id": dimensionsText(dimensions, "bcs_cluster_id"),
		"namespace":  dimensionsText(dimensions, "namespace"),
		"name":       dimensionsText(dimensions, "service"),
		"type":       dimensionsText(dimensions, "workload_kind"),
		"pod_name":   dimensionsText(dimensions, "pod_name"),
	}
	if modelCode == "cw-K8s_Pod" && dimensionsText(dimensions, "workload_kind") == "Pod" {
		values["name"] = dimensionsText(dimensions, "workload_name")
	}
	filters := make([]enrich.InstanceAttributeFilter, 0, len(fields))
	for _, field := range fields {
		value := values[field]
		if value == "" {
			return nil, fmt.Errorf("k8s identity field %q is empty", field)
		}
		filters = append(filters, enrich.InstanceAttributeFilter{Field: field, Type: enrich.InstanceAttributeKeyword, Value: value})
	}
	return filters, nil
}

func dimensionsText(dimensions domain.DimensionMap, field string) string {
	value, ok := dimensions[field]
	if !ok {
		return ""
	}
	if text, ok := value.StringValue(); ok {
		return text
	}
	if number, ok := value.NumberValue(); ok {
		return fmt.Sprint(number)
	}
	return ""
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// The query is generated from the resolved schema. Never label observations with
// expressions, matchers, entity IDs or response labels supplied by the backend.
func timeGraphLoadMetricName(query *structured.QueryTs) string {
	if query == nil || len(query.QueryList) == 0 {
		return "__unknown__"
	}
	name := ""
	for _, item := range query.QueryList {
		if item == nil || item.FieldName == "" {
			return "__unknown__"
		}
		if name != "" && name != item.FieldName {
			return "__mixed__"
		}
		name = item.FieldName
	}
	return name
}

func timeGraphReturnedMatrixSize(matrix pl.Matrix) metric.TimeGraphLoadSize {
	size := metric.TimeGraphLoadSize{Returned: true, Series: len(matrix)}
	for _, series := range matrix {
		size.Points += len(series.Points)
		for _, label := range series.Metric {
			size.LabelBytes += len(label.Name) + len(label.Value)
		}
	}
	return size
}

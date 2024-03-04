// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"fmt"

	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// GetMustVmQueryFeatureFlag 判断该 TableID 是否强行指定为单指标单表
func GetMustVmQueryFeatureFlag(ctx context.Context, tableID string) bool {
	var (
		span oleltrace.Span
		user = GetUser(ctx)
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "check-must-vm-query-feature-flag")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("table-id", tableID, span)

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := featureFlag.FFUser(span.SpanContext().TraceID().String(), map[string]interface{}{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUid,
		"tableID":  tableID,
	})

	status := featureFlag.BoolVariation(ctx, ffUser, "must-vm-query", false)
	trace.InsertStringIntoSpan("must-vm-query-flag", fmt.Sprintf("%v:%v", ffUser.GetCustom(), status), span)

	// 根据查询时间范围判断是否满足当前时间配置
	vmDataTime := featureFlag.IntVariation(ctx, ffUser, "range-vm-query", 0)

	if vmDataTime > 0 {
		queryParams := GetQueryParams(ctx)
		status = int64(vmDataTime) < queryParams.Start
		trace.InsertIntIntoSpan("vm-data-time", vmDataTime, span)
		trace.InsertStringIntoSpan("range-vm-query-flag", fmt.Sprintf("%v:%v", ffUser.GetCustom(), status), span)
	}

	return status
}

func GetVMQueryFeatureFlag(ctx context.Context) bool {
	var (
		span oleltrace.Span
		user = GetUser(ctx)
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "check-vm-query-feature-flag")
	if span != nil {
		defer span.End()
	}

	// 增加配置的特性开关
	if GetQueryRouter().CheckVmQuery(ctx, user.SpaceUid) {
		trace.InsertStringIntoSpan("vm-query-space-uid", "true", span)
		return true
	}

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := featureFlag.FFUser(span.SpanContext().TraceID().String(), map[string]interface{}{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUid,
	})

	status := featureFlag.BoolVariation(ctx, ffUser, "vm-query", false)
	trace.InsertStringIntoSpan("vm-query-feature-flag", fmt.Sprintf("%v:%v", ffUser.GetCustom(), status), span)

	return status
}

func GetIsK8sFeatureFlag(ctx context.Context) bool {
	var (
		span oleltrace.Span
		user = GetUser(ctx)
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "check-is-k8s-feature-flag")
	if span != nil {
		defer span.End()
	}

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := featureFlag.FFUser(span.SpanContext().TraceID().String(), map[string]interface{}{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUid,
	})

	status := featureFlag.BoolVariation(ctx, ffUser, "is-k8s", false)
	trace.InsertStringIntoSpan("is-k8s", fmt.Sprintf("%v:%v", ffUser.GetCustom(), status), span)

	return status
}

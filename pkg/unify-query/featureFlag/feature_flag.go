// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package featureFlag

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func GetBkDataTableIDCheck(ctx context.Context, tableID string) bool {
	var (
		user = metadata.GetUser(ctx)
		err  error
		span *trace.Span
	)

	ctx, span = trace.NewSpan(ctx, "get-bk-data-table-id-auth-feature-flag")
	defer span.End(&err)

	u := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
		"tableID":  tableID,
	})

	span.Set("ff-user-custom", u.GetCustom())
	status := BoolVariation(ctx, u, "bk-data-table-id-auth", false)
	span.Set("ff-status", status)

	return status
}

func GetJwtAuthFeatureFlag(ctx context.Context) bool {
	var (
		user = metadata.GetUser(ctx)
		err  error
		span *trace.Span
	)

	ctx, span = trace.NewSpan(ctx, "get-jwt-auth-feature-flag")
	defer span.End(&err)

	u := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
	})

	span.Set("ff-user-custom", u.GetCustom())
	status := BoolVariation(ctx, u, "jwt-auth", false)
	span.Set("ff-status", status)

	return status
}

// GetMustVmQueryFeatureFlag 判断该 TableID 是否强行指定为单指标单表
func GetMustVmQueryFeatureFlag(ctx context.Context, tableID string) bool {
	var (
		user = metadata.GetUser(ctx)
		err  error
		span *trace.Span
	)

	ctx, span = trace.NewSpan(ctx, "check-must-query-feature-flag")
	defer span.End(&err)

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
		"tableID":  tableID,
	})

	span.Set("ff-user-custom", ffUser.GetCustom())

	// 如果匹配不到，则默认查询 vm
	status := BoolVariation(ctx, ffUser, "must-vm-query", true)

	// 根据查询时间范围判断是否满足当前时间配置
	vmDataTime := IntVariation(ctx, ffUser, "range-vm-query", 0)

	if vmDataTime > 0 {
		queryParams := metadata.GetQueryParams(ctx)
		status = int64(vmDataTime) < queryParams.Start.Unix()

		span.Set("vm-data-time", vmDataTime)
		span.Set("query-params-start", queryParams.Start)
	}

	span.Set("ff-status", status)
	return status
}

func GetIsK8sFeatureFlag(ctx context.Context) bool {
	user := metadata.GetUser(ctx)

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
	})

	status := BoolVariation(ctx, ffUser, "is-k8s", false)
	return status
}

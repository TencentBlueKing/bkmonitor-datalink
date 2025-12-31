// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// gzl 特性开关管理包，用于运行时动态控制功能开关
package featureFlag

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// gzl GetBkDataTableIDCheck 检查用户是否有权限访问指定的数据表ID
// gzl 参数:
// gzl   - ctx: 上下文，包含用户信息和追踪信息
// gzl   - tableID: 要检查的数据表ID
// gzl 返回值:
// gzl   - bool: true表示有权限，false表示无权限
func GetBkDataTableIDCheck(ctx context.Context, tableID string) bool {
	var (
		user = metadata.GetUser(ctx)
		err  error
		span *trace.Span
	)

	// gzl 创建追踪span，用于记录特性开关检查过程
	ctx, span = trace.NewSpan(ctx, "get-bk-data-table-id-auth-feature-flag")
	defer span.End(&err)

	// gzl 创建特性开关用户对象，包含用户信息和表ID
	u := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
		"tableID":  tableID,
	})

	// gzl 记录用户自定义信息到追踪span
	span.Set("ff-user-custom", u.GetCustom())
	// gzl 调用特性开关服务判断权限状态
	status := BoolVariation(ctx, u, "bk-data-table-id-auth", false)
	// gzl 记录最终状态到追踪span
	span.Set("ff-status", status)

	return status
}

// GetMustVmQueryFeatureFlag 判断该 TableID 是否强行指定为单指标单表
// gzl 参数:
// gzl   - ctx: 上下文，包含用户信息和查询参数
// gzl   - tableID: 要检查的数据表ID
// gzl 返回值:
// gzl   - bool: true表示强制使用VM查询，false表示使用默认查询方式
func GetMustVmQueryFeatureFlag(ctx context.Context, tableID string) bool {
	var (
		user = metadata.GetUser(ctx)
		err  error
		span *trace.Span
	)

	// gzl 创建追踪span，用于记录VM查询特性开关检查过程
	ctx, span = trace.NewSpan(ctx, "check-must-query-feature-flag")
	defer span.End(&err)

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
		"tableID":  tableID,
	})

	// gzl 记录用户自定义信息到追踪span
	span.Set("ff-user-custom", ffUser.GetCustom())

	// 如果匹配不到，则默认查询 vm
	status := BoolVariation(ctx, ffUser, "must-vm-query", true)

	// 根据查询时间范围判断是否满足当前时间配置
	vmDataTime := IntVariation(ctx, ffUser, "range-vm-query", 0)

	// gzl 如果设置了时间阈值，需要根据查询时间范围进行判断
	if vmDataTime > 0 {
		queryParams := metadata.GetQueryParams(ctx)
		// gzl 只有当查询开始时间晚于时间阈值时才使用VM查询
		status = int64(vmDataTime) < queryParams.Start.Unix()

		// gzl 记录时间相关参数到追踪span
		span.Set("vm-data-time", vmDataTime)
		span.Set("query-params-start", queryParams.Start)
	}

	// gzl 记录最终状态到追踪span
	span.Set("ff-status", status)
	return status
}

// gzl GetIsK8sFeatureFlag 判断当前是否在Kubernetes环境中运行
// gzl 参数:
// gzl   - ctx: 上下文，包含用户信息
// gzl 返回值:
// gzl   - bool: true表示在K8s环境中，false表示不在K8s环境中
func GetIsK8sFeatureFlag(ctx context.Context) bool {
	user := metadata.GetUser(ctx)

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
	})

	// gzl 调用特性开关服务判断K8s环境状态
	status := BoolVariation(ctx, ffUser, "is-k8s", false)
	return status
}

// gzl GetVmRtFeatureFlag 判断是否排除VM结果表查询
// gzl 参数:
// gzl   - ctx: 上下文，包含用户信息
// gzl   - tableID: 要检查的数据表ID
// gzl 返回值:
// gzl   - bool: true表示排除VM结果表查询，false表示不排除
func GetVmRtFeatureFlag(ctx context.Context, tableID string) bool {
	var (
		user = metadata.GetUser(ctx)
		err  error
		span *trace.Span
	)

	// gzl 创建追踪span，用于记录VM结果表排除特性开关检查过程 todo
	ctx, span = trace.NewSpan(ctx, "vm-rt-exclusion-feature-flag")
	defer span.End(&err)

	// gzl 创建特性开关用户对象，包含用户信息和表ID todo
	ffUser := FFUser(user.HashID, map[string]any{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUID,
		"tableID":  tableID,
	})

	// gzl 记录用户自定义信息到追踪span
	span.Set("ff-user-custom", ffUser.GetCustom())
	// gzl 调用特性开关服务判断是否排除VM结果表查询
	status := BoolVariation(ctx, ffUser, "exclusion-vm-rt", false)
	// gzl 记录最终状态到追踪span
	span.Set("ff-status", status)

	return status
}

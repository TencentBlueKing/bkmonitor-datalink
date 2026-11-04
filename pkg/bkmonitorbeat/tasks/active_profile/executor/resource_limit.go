// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// resourceLimitCgroupPrefix 是 libgse 按主配置 resource_limit 为 Beat 创建的 cgroup 名前缀。
const resourceLimitCgroupPrefix = "collector-"

type outsideResourceLimitKey struct{}

// withOutsideResourceLimit 标记本次采集拉起的外部进程要移出 Beat 的 resource_limit cgroup。
// 只用于关闭“资源不足时停止”的任务：用户选择无条件采集，外部进程不再与 Beat 共用 CPU 和
// 内存上限；Beat 进程本身仍在限制内。
func withOutsideResourceLimit(ctx context.Context) context.Context {
	return context.WithValue(ctx, outsideResourceLimitKey{}, true)
}

// leaveResourceLimit 把刚启动的外部进程移到 Beat 所在 resource_limit cgroup 的上一级。
// 移不出时进程留在原 cgroup 继续执行，只记告警。
func leaveResourceLimit(ctx context.Context, pid int) {
	if outside, _ := ctx.Value(outsideResourceLimitKey{}).(bool); !outside {
		return
	}
	if err := moveOutOfResourceLimit(pid); err != nil {
		logger.Warnf("[active_profile] pid=%d stays inside the resource limit cgroup: %v", pid, err)
	}
}

// preferChildOOMKill 让"资源不足时停止"打开的任务拉起的外部进程与 Beat 共处同一内存上限时，
// 内核 OOM 优先选中它们而不是 Beat。写不进去时只记告警。
func preferChildOOMKill(ctx context.Context, pid int) {
	if outside, _ := ctx.Value(outsideResourceLimitKey{}).(bool); outside {
		return
	}
	if err := raiseOOMScore(pid); err != nil {
		logger.Warnf("[active_profile] pid=%d keeps its oom_score_adj: %v", pid, err)
	}
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package elasticsearchstore

import "time"

// QueryLayout 描述生产 Router 实际维护的查询 alias 与时间桶规则，不表示远端索引已就绪。
type QueryLayout struct {
	EventTargets        []string           `json:"event_targets"`         // EventTargets 覆盖所有 Event 时间桶。
	AlertTargets        []string           `json:"alert_targets"`         // AlertTargets 同时覆盖 Active 和 History，归档期间可能有重复快照。
	AlertLogTargets     []string           `json:"alert_log_targets"`     // AlertLogTargets 覆盖所有 AlertLog 时间桶。
	ActiveAlertTargets  []string           `json:"active_alert_targets"`  // ActiveAlertTargets 只覆盖活动索引，归档前也可能含终态文档。
	AlertHistoryTargets []string           `json:"alert_history_targets"` // AlertHistoryTargets 只覆盖已归档历史。
	TimePartition       QueryTimePartition `json:"time_partition"`        // TimePartition 描述物理桶路由，区别于查询过滤字段。
}

// QueryTimePartition 的时间桶均为 UTC 半开区间，以 Epoch 为统一起点，按 BucketDays 向下取整。
type QueryTimePartition struct {
	Timezone               string `json:"timezone"`                  // Timezone 固定为 UTC。
	Epoch                  string `json:"epoch"`                     // Epoch 是全部桶周期的共同对齐起点。
	EventBucketDays        int    `json:"event_bucket_days"`         // EventBucketDays 按 EventID 中的 received_at 路由。
	AlertHistoryBucketDays int    `json:"alert_history_bucket_days"` // AlertHistoryBucketDays 按 AlertID 中的 create_at 路由。
	AlertLogBucketDays     int    `json:"alert_log_bucket_days"`     // AlertLogBucketDays 按所属 AlertID 的 create_at 路由，而非日志时间。
}

// QueryLayout 从真实路由规则返回管理读取目标，避免消费者根据前缀自行拼接或猜测别名。
func (r *BucketRouter) QueryLayout() QueryLayout {
	return QueryLayout{
		EventTargets: []string{r.eventReadAlias()}, AlertTargets: []string{r.alertReadAlias()},
		AlertLogTargets: []string{r.alertLogReadAlias()}, ActiveAlertTargets: []string{r.activeAlertAlias()},
		AlertHistoryTargets: []string{r.alertHistoryReadAlias()},
		TimePartition: QueryTimePartition{Timezone: "UTC", Epoch: bucketEpoch.Format(time.RFC3339),
			EventBucketDays: r.eventBucketDays, AlertHistoryBucketDays: r.alertBucketDays, AlertLogBucketDays: r.alertLogBucketDays},
	}
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package mysqlstore

// QueryLayout 描述当前 MySQL schema 的实体表，不表示数据库已连接或表已初始化。
type QueryLayout struct {
	EventTable    string `json:"event_table"`     // EventTable 保存 Event payload 和独立 processing 状态。
	AlertTable    string `json:"alert_table"`     // AlertTable 保存当前 Alert 快照。
	AlertLogTable string `json:"alert_log_table"` // AlertLogTable 保存 AlertLog 时间线。
}

// EntityQueryLayout 返回与内嵌 schema 一致的读取表名；查询始终需限定 bk_tenant_id。
func EntityQueryLayout() QueryLayout {
	return QueryLayout{EventTable: "linkd_events", AlertTable: "linkd_alerts", AlertLogTable: "linkd_alert_logs"}
}

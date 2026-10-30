// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package models

import (
	"encoding/json"
	"time"
)

// ActiveProfilingExecution maps to the apm_activeprofilingexecution table.
// At-suffixed time fields use Unix milliseconds; CreatedAt and UpdatedAt use SQL DATETIME.
type ActiveProfilingExecution struct {
	ID               int             `gorm:"column:id;primary_key"`           // Auto-incrementing primary key.
	BkTenantID       string          `gorm:"column:bk_tenant_id"`             // Tenant ID.
	BkBizID          int             `gorm:"column:bk_biz_id"`                // Business ID.
	ScheduleType     string          `gorm:"column:schedule_type"`            // Scheduled or immediate execution.
	TaskID           int             `gorm:"column:task_id"`                  // Plan ID or execution record ID; legacy immediate runs may use zero.
	ScheduledAt      int64           `gorm:"column:scheduled_at"`             // Scheduled time in Unix milliseconds.
	TaskName         string          `gorm:"column:task_name"`                // Task name captured when the execution was created.
	Creator          string          `gorm:"column:creator"`                  // Creator captured when the execution was created.
	CollectionMethod string          `gorm:"column:collection_method"`        // Collection method.
	CollectionType   string          `gorm:"column:collection_type"`          // Collection type.
	ExecutionStatus  string          `gorm:"column:execution_status"`         // Status aggregated from host executions.
	StatusSummary    json.RawMessage `gorm:"column:status_summary;type:json"` // Number of hosts in each status.
	TargetCount      int             `gorm:"column:target_count"`             // Number of host rows, including placeholders.
	StartedAt        int64           `gorm:"column:started_at"`               // Earliest host start time; falls back to scheduled_at when unavailable.
	FinishedAt       *int64          `gorm:"column:finished_at"`              // Latest finished host time for a non-running execution, if available.
	Config           json.RawMessage `gorm:"column:config;type:json"`         // Execution config with merged target and per-host PID data.
	CreatedAt        time.Time       `gorm:"column:created_at"`               // Record creation time in SQL DATETIME.
	UpdatedAt        time.Time       `gorm:"column:updated_at"`               // Record update time in SQL DATETIME.
}

func (ActiveProfilingExecution) TableName() string {
	return "apm_activeprofilingexecution"
}

// ActiveProfilingHostExecution maps to the apm_activeprofilinghostexecution table.
// At-suffixed fields and EventTime use Unix milliseconds; CreatedAt and UpdatedAt use SQL DATETIME.
type ActiveProfilingHostExecution struct {
	ID           int             `gorm:"column:id;primary_key"`      // Auto-incrementing primary key.
	ExecutionID  int             `gorm:"column:execution_id"`        // Parent execution ID.
	BkBizID      int             `gorm:"column:bk_biz_id"`           // Business ID.
	ScheduledAt  int64           `gorm:"column:scheduled_at"`        // Scheduled time in Unix milliseconds.
	BkHostID     int             `gorm:"column:bk_host_id"`          // Host ID.
	IP           string          `gorm:"column:ip"`                  // Target IP from the active Event, or snapshot IP for placeholders.
	Status       string          `gorm:"column:status"`              // Host status, such as running, unknown, or a terminal status.
	StartedAt    *int64          `gorm:"column:started_at"`          // Start time from the active Event in Unix milliseconds.
	FinishedAt   *int64          `gorm:"column:finished_at"`         // Finish time from the active finished Event in Unix milliseconds.
	ErrorType    string          `gorm:"column:error_type"`          // Error type from the finished Event.
	Message      string          `gorm:"column:message"`             // Error message from the finished Event.
	ProcessNames string          `gorm:"column:process_names"`       // Process names from the finished Event.
	ProcessPIDs  string          `gorm:"column:process_pids"`        // Process PIDs from the finished Event.
	Processes    json.RawMessage `gorm:"column:processes;type:json"` // Process details from the finished Event.
	EventTime    int64           `gorm:"column:event_time"`          // Active Event timestamp in Unix milliseconds.
	UnknownAt    *int64          `gorm:"column:unknown_at"`          // Unknown deadline in Unix milliseconds; cleared after a finished Event.
	CreatedAt    time.Time       `gorm:"column:created_at"`          // Record creation time in SQL DATETIME.
	UpdatedAt    time.Time       `gorm:"column:updated_at"`          // Record update time in SQL DATETIME.
}

func (ActiveProfilingHostExecution) TableName() string {
	return "apm_activeprofilinghostexecution"
}

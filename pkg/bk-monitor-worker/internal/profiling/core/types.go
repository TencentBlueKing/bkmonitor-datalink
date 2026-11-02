// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	profilingwindow "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/window"
)

const (
	EventStarted             = "active_profiling_started"
	EventFinished            = "active_profiling_finished"
	StatusRunning            = "running"
	StatusUnknown            = "unknown"
	DefaultWindowMaxDuration = profilingwindow.DefaultMaxDuration
	DefaultWindowDelay       = profilingwindow.DefaultDelay
)

var (
	ErrInvalidEvent     = errors.New("invalid active profiling event")
	ErrUnsupportedEvent = errors.New("unsupported active profiling event")
	ErrInvalidOptions   = errors.New("invalid profiling processor options")
	// ErrNonRetryable marks a persistent processing or configuration failure that should stop the consumer.
	ErrNonRetryable = errors.New("non-retryable active profiling error")
)

// Event is the normalized representation of one active profiling Event.
type Event struct {
	Name             string
	Target           string
	Timestamp        int64
	TenantID         string
	BizID            int
	TaskID           int
	ScheduleType     string
	ScheduledAt      int64
	HostID           int
	TaskName         string
	Creator          string
	CollectionMethod string
	CollectionType   string
	Status           string
	ProcessNames     string
	ProcessPIDs      string
	Content          EventContent
}

// EventContent contains the schema-versioned event payload fields used by the
// active profiling materialization.
type EventContent struct {
	SchemaVersion       int             `json:"schema_version"`
	StartedAt           int64           `json:"started_at"`
	FinishedAt          int64           `json:"finished_at"`
	ExecutionDurationMS int64           `json:"execution_duration_ms"`
	ErrorType           string          `json:"error_type"`
	Processes           json.RawMessage `json:"processes"`
	Message             string          `json:"message"`
	Config              json.RawMessage `json:"config"`
	TargetHosts         []TargetHost    `json:"target_hosts"`
}

// TargetHost is a target host snapshot carried by started or SaaS-fallback
// finished events.
type TargetHost struct {
	HostID int    `json:"bk_host_id"`
	IP     string `json:"ip"`
}

// ExecutionKey is the database identity shared by all host events in one run.
type ExecutionKey struct {
	TenantID    string
	BizID       int
	TaskID      int
	ScheduledAt int64
}

// Store persists event changes and applies time-driven transitions. Implementations
// must make retries idempotent.
type Store interface {
	ApplyEvent(context.Context, Event, int64, WindowPolicy) error
	MarkUnknown(context.Context, int64, int) (int, error)
}

// BatchStore applies multiple events with fewer durable writes when supported.
// Processors fall back to Store.ApplyEvent for stores without batch support.
type BatchStore interface {
	ApplyEvents(context.Context, []Event, int64, WindowPolicy) error
}

// WindowPolicy defines the collection duration cap and late-event grace period.
type WindowPolicy = profilingwindow.Policy

// Options configures window calculation, unknown-state scanning, and error reporting.
type Options struct {
	WindowMaxDuration time.Duration // Maximum collection duration for one host.
	WindowDelay       time.Duration // Grace period for late finished Events.

	UnknownScanInterval  time.Duration // Interval between expired-window scans.
	UnknownScanBatchSize int           // Maximum executions scanned in one batch.
	Now                  func() time.Time
	OnError              func(error)
}

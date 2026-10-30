// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	profilingwindow "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/window"
)

func TestDurationMillis(t *testing.T) {
	tests := []struct {
		name        string
		config      json.RawMessage
		maxDuration time.Duration
		want        int64
	}{
		{
			name:        "duration below maximum",
			config:      json.RawMessage(`{"collector_config":{"duration_sec":30}}`),
			maxDuration: time.Minute,
			want:        30_000,
		},
		{
			name:        "duration capped at maximum",
			config:      json.RawMessage(`{"collector_config":{"duration_sec":120}}`),
			maxDuration: time.Minute,
			want:        60_000,
		},
		{
			name:        "duration string",
			config:      json.RawMessage(`{"collector_config":{"duration_sec":"15"}}`),
			maxDuration: time.Minute,
			want:        15_000,
		},
		{
			name:        "missing duration",
			config:      json.RawMessage(`{"collector_config":{}}`),
			maxDuration: time.Minute,
			want:        0,
		},
		{
			name:        "negative duration",
			config:      json.RawMessage(`{"collector_config":{"duration_sec":-1}}`),
			maxDuration: time.Minute,
			want:        0,
		},
		{
			name:        "invalid config",
			config:      json.RawMessage(`{`),
			maxDuration: time.Minute,
			want:        0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := profilingwindow.CollectionDuration(test.config, test.maxDuration).Milliseconds(); got != test.want {
				t.Fatalf("CollectionDuration() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestStartedEventWindowDeadlineAndBoundary(t *testing.T) {
	const startedAt int64 = 1_700_000_000_000
	config := json.RawMessage(`{"collector_config":{"duration_sec":120}}`)
	policy := core.WindowPolicy{MaxDuration: time.Minute, Delay: 5 * time.Second}
	wantDeadline := startedAt + policy.MaxDuration.Milliseconds() + policy.Delay.Milliseconds()
	event := core.Event{
		Name:      core.EventStarted,
		Status:    core.StatusRunning,
		Timestamp: startedAt,
		Content: core.EventContent{
			StartedAt: startedAt,
			Config:    config,
		},
	}

	beforeDeadline := eventHost(event, 1, time.UnixMilli(startedAt), wantDeadline-1, policy)
	if beforeDeadline.Status != core.StatusRunning {
		t.Fatalf("status before deadline = %q, want %q", beforeDeadline.Status, core.StatusRunning)
	}
	if beforeDeadline.UnknownAt == nil || *beforeDeadline.UnknownAt != wantDeadline {
		t.Fatalf("unknown_at before deadline = %v, want %d", beforeDeadline.UnknownAt, wantDeadline)
	}

	atDeadline := eventHost(event, 1, time.UnixMilli(startedAt), wantDeadline, policy)
	if atDeadline.Status != core.StatusUnknown {
		t.Fatalf("status at deadline = %q, want %q", atDeadline.Status, core.StatusUnknown)
	}
}

func TestPlaceholderHostWindowStartsAtScheduledAt(t *testing.T) {
	const scheduledAt int64 = 1_700_000_000_000
	policy := core.WindowPolicy{MaxDuration: time.Minute, Delay: 5 * time.Second}
	wantDeadline := scheduledAt + 10_000 + policy.Delay.Milliseconds()
	event := core.Event{
		BizID:       2,
		ScheduledAt: scheduledAt,
		Content: core.EventContent{
			Config: json.RawMessage(`{"collector_config":{"duration_sec":10}}`),
		},
	}
	target := core.TargetHost{HostID: 3, IP: "127.0.0.1"}

	beforeDeadline := placeholderHost(event, 4, target, time.UnixMilli(scheduledAt), wantDeadline-1, policy)
	if beforeDeadline.Status != core.StatusRunning {
		t.Fatalf("placeholder status before deadline = %q, want %q", beforeDeadline.Status, core.StatusRunning)
	}
	if beforeDeadline.UnknownAt == nil || *beforeDeadline.UnknownAt != wantDeadline {
		t.Fatalf("placeholder unknown_at = %v, want %d", beforeDeadline.UnknownAt, wantDeadline)
	}

	atDeadline := placeholderHost(event, 4, target, time.UnixMilli(scheduledAt), wantDeadline, policy)
	if atDeadline.Status != core.StatusUnknown {
		t.Fatalf("placeholder status at deadline = %q, want %q", atDeadline.Status, core.StatusUnknown)
	}
}

func TestFinishedEventClosesWindow(t *testing.T) {
	const startedAt int64 = 1_700_000_000_000
	event := core.Event{
		Name:   core.EventFinished,
		Status: "failed",
		Content: core.EventContent{
			StartedAt:           startedAt,
			FinishedAt:          startedAt + 20_000,
			ExecutionDurationMS: 20_000,
			Processes:           json.RawMessage(`[]`),
			Message:             "collector failed",
		},
	}

	host := eventHost(event, 1, time.UnixMilli(startedAt+20_000), startedAt+20_000, core.WindowPolicy{
		MaxDuration: time.Minute,
		Delay:       5 * time.Second,
	})
	if host.Status != "failed" {
		t.Fatalf("finished host status = %q, want failed", host.Status)
	}
	if host.UnknownAt != nil {
		t.Fatalf("finished host unknown_at = %v, want nil", host.UnknownAt)
	}
}

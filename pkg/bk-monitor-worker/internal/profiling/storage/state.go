// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/models"
	profilingwindow "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/window"
)

// State derivation is shared by the MySQL and in-memory stores.
type executionRow struct {
	ID              int
	Key             core.ExecutionKey
	ScheduledAt     int64
	ExecutionStatus string
	StatusSummary   json.RawMessage
	TargetCount     int
	StartedAt       int64
	FinishedAt      *int64
	Config          json.RawMessage
}

type executionBatch struct {
	key    core.ExecutionKey
	events []core.Event
}

func groupEventsByExecution(events []core.Event) ([]executionBatch, error) {
	batches := make([]executionBatch, 0)
	indexes := make(map[core.ExecutionKey]int)
	for _, event := range events {
		if err := core.ValidateEvent(event); err != nil {
			return nil, err
		}
		key := core.ExecutionKey{
			TenantID: event.TenantID, BizID: event.BizID, TaskID: event.TaskID, ScheduledAt: event.ScheduledAt,
		}
		index, exists := indexes[key]
		if !exists {
			index = len(batches)
			indexes[key] = index
			batches = append(batches, executionBatch{key: key})
		}
		batches[index].events = append(batches[index].events, event)
	}
	return batches, nil
}

func placeholderHost(
	event core.Event,
	executionID int,
	target core.TargetHost,
	now time.Time,
	nowMS int64,
	policy core.WindowPolicy,
) models.ActiveProfilingHostExecution {
	// A snapshot host has no started Event yet, so its timeout window starts at scheduled_at.
	collectWindow := profilingwindow.Start(event.ScheduledAt, event.Content.Config, policy, nowMS)
	unknownAt := collectWindow.DeadlineAtMS()
	status := core.StatusRunning
	if collectWindow.IsExpired() {
		status = core.StatusUnknown
	}
	return models.ActiveProfilingHostExecution{
		ExecutionID:  executionID,
		BkBizID:      event.BizID,
		ScheduledAt:  event.ScheduledAt,
		BkHostID:     target.HostID,
		IP:           target.IP,
		Status:       status,
		ErrorType:    "",
		Message:      "",
		ProcessNames: "",
		ProcessPIDs:  "",
		Processes:    json.RawMessage("[]"),
		EventTime:    0,
		UnknownAt:    &unknownAt,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func eventHost(
	event core.Event,
	executionID int,
	now time.Time,
	nowMS int64,
	policy core.WindowPolicy,
) models.ActiveProfilingHostExecution {
	host := models.ActiveProfilingHostExecution{
		ExecutionID:  executionID,
		BkBizID:      event.BizID,
		ScheduledAt:  event.ScheduledAt,
		BkHostID:     event.HostID,
		IP:           event.Target,
		Status:       event.Status,
		ErrorType:    "",
		Message:      "",
		ProcessNames: "",
		ProcessPIDs:  "",
		Processes:    json.RawMessage("[]"),
		EventTime:    event.Timestamp,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if len(host.Processes) == 0 {
		host.Processes = json.RawMessage("[]")
	}
	startedAt := event.Content.StartedAt
	collectWindow := profilingwindow.New(startedAt, event.Content.Config, policy)
	if event.Name == core.EventStarted {
		// Started Events use the actual start time; an already expired window is stored as unknown.
		collectWindow, _ = collectWindow.ExpireAt(nowMS)
		deadline := collectWindow.DeadlineAtMS()
		status := core.StatusRunning
		if collectWindow.IsExpired() {
			status = core.StatusUnknown
		}
		host.Status = status
		host.StartedAt = &startedAt
		host.UnknownAt = &deadline
		return host
	}
	// Finished Events are terminal and clear the deadline so later scans cannot mark the host unknown.
	collectWindow = collectWindow.Finish()
	finishedAt := event.Content.FinishedAt
	host.Status = event.Status
	host.StartedAt = &startedAt
	host.FinishedAt = &finishedAt
	host.ErrorType = event.Content.ErrorType
	host.Message = event.Content.Message
	host.Processes = event.Content.Processes
	if len(host.Processes) == 0 {
		host.Processes = json.RawMessage("[]")
	}
	host.ProcessNames = event.ProcessNames
	host.ProcessPIDs = event.ProcessPIDs
	host.UnknownAt = collectWindow.Deadline()
	return host
}
func shouldApplyEvent(current models.ActiveProfilingHostExecution, event core.Event) bool {
	// Started Events update only active or placeholder rows and must be newer than
	// the stored Event. Finished Events can close active rows; terminal rows accept
	// only newer Events so late results cannot overwrite a newer terminal state.
	currentStarted := current.Status == core.StatusRunning ||
		current.Status == core.StatusUnknown ||
		(current.StartedAt == nil && current.FinishedAt == nil && current.EventTime == 0)
	if event.Name == core.EventStarted {
		return currentStarted && event.Timestamp > current.EventTime
	}
	if currentStarted {
		return current.EventTime == 0 || event.Timestamp >= current.EventTime
	}
	return event.Timestamp > current.EventTime
}

type executionAggregate struct {
	status        string
	statusSummary string
	targetCount   int
	startedAt     int64
	finishedAt    *int64
}

type executionAggregateStats struct {
	counts        map[string]int
	minStartedAt  int64
	maxFinishedAt int64
	hasStartedAt  bool
	hasFinishedAt bool
}

func (s *executionAggregateStats) addHost(status string, startedAt, finishedAt *int64) {
	if s.counts == nil {
		s.counts = make(map[string]int, 8)
	}
	s.counts[status]++
	if startedAt != nil && (!s.hasStartedAt || *startedAt < s.minStartedAt) {
		s.minStartedAt = *startedAt
		s.hasStartedAt = true
	}
	// Unknown means the window expired without a finished result, so it does not
	// contribute to the execution finish time.
	if status != core.StatusUnknown && finishedAt != nil &&
		(!s.hasFinishedAt || *finishedAt > s.maxFinishedAt) {
		s.maxFinishedAt = *finishedAt
		s.hasFinishedAt = true
	}
}

func (s executionAggregateStats) aggregate(execution executionRow) (executionAggregate, error) {
	if len(s.counts) == 0 {
		return executionAggregate{
			status:        core.StatusRunning,
			statusSummary: `{}`,
			startedAt:     execution.ScheduledAt,
		}, nil
	}
	return aggregateExecutionFromStats(
		execution,
		s.counts,
		s.minStartedAt,
		s.hasStartedAt,
		s.maxFinishedAt,
		s.hasFinishedAt,
	)
}

func aggregateExecution(
	execution executionRow,
	hosts map[int]models.ActiveProfilingHostExecution,
) (executionAggregate, error) {
	if len(hosts) == 0 {
		return executionAggregate{}, fmt.Errorf("execution %d has no host rows to aggregate", execution.ID)
	}
	var stats executionAggregateStats
	for _, host := range hosts {
		stats.addHost(host.Status, host.StartedAt, host.FinishedAt)
	}
	return stats.aggregate(execution)
}

func aggregateExecutionFromStats(
	execution executionRow,
	counts map[string]int,
	minStartedAt int64,
	hasStartedAt bool,
	maxFinishedAt int64,
	hasFinishedAt bool,
) (executionAggregate, error) {
	if len(counts) == 0 {
		return executionAggregate{
			status:        core.StatusRunning,
			statusSummary: `{}`,
			startedAt:     execution.ScheduledAt,
		}, nil
	}
	allSame := len(counts) == 1
	firstStatus := ""
	anyRunning := counts[core.StatusRunning] > 0
	anySuccess := counts["succeed"] > 0 || counts["partial_succeed"] > 0
	for status := range counts {
		firstStatus = status
		break
	}
	status := "failed"
	// Aggregate status precedence: running if any host is running; preserve the
	// status when all hosts match; otherwise use partial_succeed when any host
	// succeeded and failed for other mixed states.
	switch {
	case anyRunning:
		status = core.StatusRunning
	case allSame:
		status = firstStatus
	case anySuccess:
		status = "partial_succeed"
	}
	if !hasStartedAt {
		// The schema requires started_at. Use scheduled_at when all rows are placeholders.
		minStartedAt = execution.ScheduledAt
	}
	var finishedAt *int64
	if status != core.StatusRunning && hasFinishedAt {
		finishedAt = &maxFinishedAt
	}
	statusSummary, err := json.Marshal(counts)
	if err != nil {
		return executionAggregate{}, fmt.Errorf("encode status summary execution_id=%d: %w", execution.ID, err)
	}
	return executionAggregate{
		status:        status,
		statusSummary: string(statusSummary),
		targetCount:   sumStatusCounts(counts),
		startedAt:     minStartedAt,
		finishedAt:    finishedAt,
	}, nil
}

func sumStatusCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

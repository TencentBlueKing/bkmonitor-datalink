// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	ensureExecutionSQL = `
INSERT INTO apm_activeprofilingexecution (
	bk_tenant_id, bk_biz_id, schedule_type, task_id, scheduled_at, task_name,
	creator, collection_method, collection_type, execution_status, status_summary,
	target_count, started_at, config, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'running', '{}', 0, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`

	selectExecutionHostsSQL = `SELECT id, execution_id, bk_biz_id, scheduled_at,
	bk_host_id, ip, status, started_at, finished_at, error_type, message,
	process_names, process_pids, processes, event_time, unknown_at, created_at, updated_at
FROM apm_activeprofilinghostexecution WHERE execution_id = ? ORDER BY bk_host_id FOR UPDATE`
)

// ApplyEvent preserves the single-event Store API.
func (s *MySQLStore) ApplyEvent(
	ctx context.Context,
	event core.Event,
	nowMS int64,
	policy core.WindowPolicy,
) error {
	return s.ApplyEvents(ctx, []core.Event{event}, nowMS, policy)
}

// ApplyEvents groups writes by execution, persists changed host rows, and
// refreshes the execution summary before returning.
func (s *MySQLStore) ApplyEvents(
	ctx context.Context,
	events []core.Event,
	nowMS int64,
	policy core.WindowPolicy,
) error {
	batches, err := groupEventsByExecution(events)
	if err != nil {
		return err
	}
	for _, batch := range batches {
		if err := s.applyExecutionEvents(ctx, batch.key, batch.events, nowMS, policy); err != nil {
			return err
		}
	}
	return nil
}

func (s *MySQLStore) applyExecutionEvents(
	ctx context.Context,
	key core.ExecutionKey,
	events []core.Event,
	nowMS int64,
	policy core.WindowPolicy,
) (retErr error) {
	started := time.Now()
	phase := "acquire connection"
	executionID := 0
	defer func() {
		retErr = classifyMySQLError(retErr)
		if retErr != nil {
			logger.Debugf(
				"[profiling] MySQL event batch failed execution_id=%d tenant_id=%s biz_id=%d task_id=%d scheduled_at=%d phase=%s event_count=%d cost=%s err=%s",
				executionID, key.TenantID, key.BizID, key.TaskID, key.ScheduledAt,
				phase, len(events), time.Since(started), retErr,
			)
		}
	}()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire active profiling event connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	now := time.Now()
	phase = "ensure execution row"
	executionID, err = ensureExecutionRow(ctx, conn, events[0], now)
	if err != nil {
		return err
	}
	phase = "acquire execution lock"
	if err := acquireExecutionLock(ctx, conn, executionID); err != nil {
		return err
	}
	defer releaseExecutionLock(conn, executionID)
	phase = "begin transaction"
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin active profiling event transaction execution_id=%d: %w", executionID, err)
	}
	defer func() { _ = tx.Rollback() }()

	phase = "lock execution row"
	execution, err := scanExecution(tx.QueryRowContext(ctx, selectExecutionByIDLockSQL, executionID))
	if err != nil {
		return fmt.Errorf("read active profiling execution id=%d: %w", executionID, err)
	}
	phase = "load execution hosts"
	hostStates, err := loadExecutionHosts(ctx, tx, executionID)
	if err != nil {
		return err
	}

	phase = "prepare event changes"
	changes, err := prepareExecutionEventChanges(execution, events, hostStates, now, nowMS, policy)
	if err != nil {
		return err
	}
	aggregate, err := aggregateExecution(execution, changes.hostStates)
	if err != nil {
		return err
	}
	for hostID, host := range changes.hostStates {
		host.ExecutionID = executionID
		changes.hostStates[hostID] = host
	}
	for i := range changes.hosts {
		changes.hosts[i].ExecutionID = executionID
	}
	updateSummary := executionAggregateChanged(execution, aggregate)
	phase = "persist event changes"
	if err := persistExecutionEventChanges(ctx, tx, execution, changes, aggregate, updateSummary, now); err != nil {
		return err
	}
	phase = "commit transaction"
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit active profiling event transaction execution_id=%d: %w", executionID, err)
	}
	for _, host := range changes.hosts {
		if previous, exists := changes.previousHosts[host.BkHostID]; exists {
			logWindowChange(executionID, &previous, host)
		} else {
			logWindowChange(executionID, nil, host)
		}
	}
	logger.Debugf(
		"[profiling] MySQL event batch persisted execution_id=%d tenant_id=%s biz_id=%d task_id=%d scheduled_at=%d event_count=%d changed_hosts=%d execution_status=%s target_count=%d cost=%s",
		execution.ID, key.TenantID, key.BizID, key.TaskID, key.ScheduledAt,
		len(events), len(changes.hosts), aggregate.status, aggregate.targetCount, time.Since(started),
	)
	return nil
}

func ensureExecutionRow(
	ctx context.Context,
	conn *sql.Conn,
	event core.Event,
	now time.Time,
) (int, error) {
	initialConfig, err := validJSON(event.Content.Config, `{}`)
	if err != nil {
		return 0, fmt.Errorf("validate Event config: %w", err)
	}
	result, err := conn.ExecContext(ctx, ensureExecutionSQL,
		event.TenantID, event.BizID, event.ScheduleType, event.TaskID,
		event.ScheduledAt, event.TaskName, event.Creator, event.CollectionMethod,
		event.CollectionType, event.ScheduledAt, initialConfig, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("ensure active profiling execution row: %w", err)
	}
	executionID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read active profiling execution id: %w", err)
	}
	return int(executionID), nil
}

type executionEventChanges struct {
	config        string
	hosts         []models.ActiveProfilingHostExecution
	hostStates    map[int]models.ActiveProfilingHostExecution
	previousHosts map[int]models.ActiveProfilingHostExecution
}

func prepareExecutionEventChanges(
	execution executionRow,
	events []core.Event,
	existingHosts map[int]models.ActiveProfilingHostExecution,
	now time.Time,
	nowMS int64,
	policy core.WindowPolicy,
) (executionEventChanges, error) {
	hosts := make(map[int]models.ActiveProfilingHostExecution, len(existingHosts))
	previousHosts := make(map[int]models.ActiveProfilingHostExecution, len(existingHosts))
	for hostID, host := range existingHosts {
		hosts[hostID] = host
		previousHosts[hostID] = host
	}
	changedHostIDs := make(map[int]struct{})
	config := string(execution.Config)
	for _, event := range events {
		addedHostIDs := make(map[int]struct{}, len(event.Content.TargetHosts)+1)
		// Add missing snapshot hosts first so the execution summary includes every target.
		for _, target := range event.Content.TargetHosts {
			if target.HostID == event.HostID {
				continue
			}
			if _, exists := hosts[target.HostID]; exists {
				continue
			}
			hosts[target.HostID] = placeholderHost(event, execution.ID, target, now, nowMS, policy)
			addedHostIDs[target.HostID] = struct{}{}
			changedHostIDs[target.HostID] = struct{}{}
		}

		current, exists := hosts[event.HostID]
		if !exists {
			current = eventHost(event, execution.ID, now, nowMS, policy)
			hosts[event.HostID] = current
			addedHostIDs[event.HostID] = struct{}{}
			changedHostIDs[event.HostID] = struct{}{}
		} else if shouldApplyEvent(current, event) {
			updated := eventHost(event, execution.ID, now, nowMS, policy)
			updated.ID = current.ID
			updated.CreatedAt = current.CreatedAt
			hosts[event.HostID] = updated
			changedHostIDs[event.HostID] = struct{}{}
		}

		mergedConfig, err := mergeConfig(
			json.RawMessage(config), event.Content.Config, addedHostIDs,
			event.Content.TargetHosts, event.HostID,
		)
		if err != nil {
			return executionEventChanges{}, fmt.Errorf("%w: merge execution config: %w", core.ErrNonRetryable, err)
		}
		config = mergedConfig
	}

	changedIDs := make([]int, 0, len(changedHostIDs))
	for hostID := range changedHostIDs {
		changedIDs = append(changedIDs, hostID)
	}
	sort.Ints(changedIDs)
	changedHosts := make([]models.ActiveProfilingHostExecution, 0, len(changedIDs))
	for _, hostID := range changedIDs {
		changedHosts = append(changedHosts, hosts[hostID])
	}
	return executionEventChanges{
		config:        config,
		hosts:         changedHosts,
		hostStates:    hosts,
		previousHosts: previousHosts,
	}, nil
}

func persistExecutionEventChanges(
	ctx context.Context,
	runner sqlExecer,
	execution executionRow,
	changes executionEventChanges,
	aggregate executionAggregate,
	updateSummary bool,
	now time.Time,
) error {
	if len(changes.hosts) > 0 {
		if err := upsertHosts(ctx, runner, changes.hosts); err != nil {
			return err
		}
	}
	if !bytes.Equal(execution.Config, []byte(changes.config)) {
		if _, err := runner.ExecContext(ctx, `UPDATE apm_activeprofilingexecution
			SET config = ?, updated_at = ? WHERE id = ?`, changes.config, now, execution.ID); err != nil {
			return fmt.Errorf("update active profiling execution config id=%d: %w", execution.ID, err)
		}
	}
	if updateSummary {
		if _, err := runner.ExecContext(ctx, updateExecutionSummarySQL,
			aggregate.status, aggregate.statusSummary, aggregate.targetCount,
			aggregate.startedAt, nullableInt(aggregate.finishedAt), now, execution.ID,
		); err != nil {
			return fmt.Errorf("update active profiling summary id=%d: %w", execution.ID, err)
		}
	}
	return nil
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadExecutionHosts(
	ctx context.Context,
	queryer sqlQueryer,
	executionID int,
) (map[int]models.ActiveProfilingHostExecution, error) {
	rows, err := queryer.QueryContext(ctx, selectExecutionHostsSQL, executionID)
	if err != nil {
		return nil, fmt.Errorf("load active profiling hosts execution_id=%d: %w", executionID, err)
	}
	defer func() { _ = rows.Close() }()

	hosts := make(map[int]models.ActiveProfilingHostExecution)
	for rows.Next() {
		var host models.ActiveProfilingHostExecution
		var startedAt, finishedAt, unknownAt sql.NullInt64
		var processes []byte
		if err := rows.Scan(
			&host.ID, &host.ExecutionID, &host.BkBizID, &host.ScheduledAt,
			&host.BkHostID, &host.IP, &host.Status, &startedAt, &finishedAt,
			&host.ErrorType, &host.Message, &host.ProcessNames, &host.ProcessPIDs,
			&processes, &host.EventTime, &unknownAt, &host.CreatedAt, &host.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active profiling host execution_id=%d: %w", executionID, err)
		}
		host.StartedAt = nullInt64(startedAt)
		host.FinishedAt = nullInt64(finishedAt)
		host.UnknownAt = nullInt64(unknownAt)
		host.Processes = append(json.RawMessage(nil), processes...)
		hosts[host.BkHostID] = host
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read active profiling hosts execution_id=%d: %w", executionID, err)
	}
	return hosts, nil
}

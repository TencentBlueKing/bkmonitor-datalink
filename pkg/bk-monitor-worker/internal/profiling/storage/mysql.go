// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/models"
	workerMysql "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	updateExecutionSummarySQL = `UPDATE apm_activeprofilingexecution SET
execution_status = ?, status_summary = ?, target_count = ?, started_at = ?,
finished_at = ?, updated_at = ? WHERE id = ?`

	selectExecutionByIDLockSQL = `SELECT id, bk_tenant_id, bk_biz_id, task_id,
	scheduled_at, execution_status, status_summary,
	target_count, started_at, finished_at, config
FROM apm_activeprofilingexecution WHERE id = ? FOR UPDATE`
)

// MySQLStore persists host state and summaries derived from Kafka events.
// Writers for one execution coordinate with a MySQL advisory lock.
type MySQLStore struct {
	db *sql.DB
}

// NewMySQLStore creates a store over a caller-owned database/sql connection pool.
func NewMySQLStore(db *sql.DB) (*MySQLStore, error) {
	if db == nil {
		return nil, errors.New("mysql db is required")
	}
	return &MySQLStore{db: db}, nil
}

// NewConfiguredMySQLStore uses the worker's configured MySQL connection pool.
func NewConfiguredMySQLStore() (*MySQLStore, error) {
	configured := workerMysql.GetDBSession().DB
	if configured == nil {
		return nil, errors.New("configured mysql session is unavailable")
	}
	db := configured.DB()
	return NewMySQLStore(db)
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func acquireExecutionLock(ctx context.Context, conn *sql.Conn, executionID int) error {
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 10)`, executionLockName(executionID)).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire active profiling execution lock id=%d: %w", executionID, err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return fmt.Errorf("acquire active profiling execution lock id=%d timed out", executionID)
	}
	return nil
}

func releaseExecutionLock(conn *sql.Conn, executionID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, executionLockName(executionID)).Scan(&released); err == nil && released.Valid && released.Int64 == 1 {
		return
	}
	// A named lock belongs to the MySQL session. Discard this connection if the
	// explicit release failed, so it cannot return to the pool while still locked.
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); err != nil {
		logger.Warnf("discard connection after active profiling lock release failure execution_id=%d: %s", executionID, err)
	}
}

func executionLockName(executionID int) string {
	return fmt.Sprintf("active-profiling:%d", executionID)
}

func upsertHosts(ctx context.Context, runner sqlExecer, hosts []models.ActiveProfilingHostExecution) error {
	const columnsPerRow = 17
	const batchSize = 300
	for start := 0; start < len(hosts); start += batchSize {
		end := min(start+batchSize, len(hosts))
		chunk := hosts[start:end]
		var query strings.Builder
		query.WriteString(`INSERT INTO apm_activeprofilinghostexecution (
			execution_id, bk_biz_id, scheduled_at, bk_host_id, ip, status, started_at,
			finished_at, error_type, message, process_names, process_pids,
			processes, event_time, unknown_at, created_at, updated_at
		) VALUES `)
		args := make([]any, 0, len(chunk)*columnsPerRow)
		for index, host := range chunk {
			if index > 0 {
				query.WriteByte(',')
			}
			query.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
			processes, err := validJSON(host.Processes, `[]`)
			if err != nil {
				return err
			}
			args = append(args,
				host.ExecutionID, host.BkBizID, host.ScheduledAt, host.BkHostID, host.IP,
				host.Status, nullableInt(host.StartedAt), nullableInt(host.FinishedAt),
				host.ErrorType, host.Message, host.ProcessNames, host.ProcessPIDs, processes,
				host.EventTime, nullableInt(host.UnknownAt), host.CreatedAt, host.UpdatedAt,
			)
		}
		query.WriteString(` ON DUPLICATE KEY UPDATE
			ip=VALUES(ip), status=VALUES(status), started_at=VALUES(started_at),
			finished_at=VALUES(finished_at), error_type=VALUES(error_type),
			message=VALUES(message), process_names=VALUES(process_names),
			process_pids=VALUES(process_pids), processes=VALUES(processes),
			event_time=VALUES(event_time), unknown_at=VALUES(unknown_at), updated_at=VALUES(updated_at)`)
		if _, err := runner.ExecContext(ctx, query.String(), args...); err != nil {
			hostIDs := make([]int, len(chunk))
			for index, host := range chunk {
				hostIDs[index] = host.BkHostID
			}
			logger.Debugf(
				"[profiling] MySQL host upsert failed execution_id=%d biz_id=%d scheduled_at=%d hosts=%d host_ids=%v err=%s",
				chunk[0].ExecutionID, chunk[0].BkBizID, chunk[0].ScheduledAt, len(chunk), hostIDs, err,
			)
			return fmt.Errorf("upsert active profiling host batch execution_id=%d hosts=%d: %w", chunk[0].ExecutionID, len(chunk), err)
		}
	}
	return nil
}

func logWindowChange(executionID int, previous *models.ActiveProfilingHostExecution, host models.ActiveProfilingHostExecution) {
	wasRunning := previous != nil && previous.Status == core.StatusRunning && previous.UnknownAt != nil
	if host.Status == core.StatusRunning && host.UnknownAt != nil {
		if !wasRunning {
			logger.Debugf("[profiling] window opened execution_id=%d host_id=%d deadline_ms=%d", executionID, host.BkHostID, *host.UnknownAt)
		} else if *previous.UnknownAt != *host.UnknownAt {
			logger.Debugf("[profiling] window updated execution_id=%d host_id=%d deadline_ms=%d", executionID, host.BkHostID, *host.UnknownAt)
		}
		return
	}
	if wasRunning {
		logger.Debugf("[profiling] window closed execution_id=%d host_id=%d status=%s", executionID, host.BkHostID, host.Status)
	} else if previous == nil && host.Status == core.StatusUnknown && host.UnknownAt != nil {
		logger.Debugf("[profiling] window expired execution_id=%d host_id=%d deadline_ms=%d", executionID, host.BkHostID, *host.UnknownAt)
	}
}

func scanExecution(row scanner) (executionRow, error) {
	var execution executionRow
	var statusSummary []byte
	var finishedAt sql.NullInt64
	err := row.Scan(
		&execution.ID, &execution.Key.TenantID, &execution.Key.BizID, &execution.Key.TaskID,
		&execution.ScheduledAt, &execution.ExecutionStatus, &statusSummary,
		&execution.TargetCount, &execution.StartedAt, &finishedAt, &execution.Config,
	)
	if err != nil {
		return executionRow{}, err
	}
	execution.StatusSummary = append(json.RawMessage(nil), statusSummary...)
	execution.FinishedAt = nullInt64(finishedAt)
	execution.Key.ScheduledAt = execution.ScheduledAt
	return execution, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func nullInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func nullableInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func executionAggregateChanged(execution executionRow, aggregate executionAggregate) bool {
	var currentSummary, nextSummary map[string]int
	if json.Unmarshal(execution.StatusSummary, &currentSummary) != nil ||
		json.Unmarshal([]byte(aggregate.statusSummary), &nextSummary) != nil {
		return true
	}
	return execution.ExecutionStatus != aggregate.status ||
		!maps.Equal(currentSummary, nextSummary) ||
		execution.TargetCount != aggregate.targetCount ||
		execution.StartedAt != aggregate.startedAt ||
		!equalNullableInt(execution.FinishedAt, aggregate.finishedAt)
}

func equalNullableInt(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ core.Store = (*MySQLStore)(nil)
var _ core.BatchStore = (*MySQLStore)(nil)

// classifyMySQLError maps MySQL errors to profiling error categories while preserving the original error.
func classifyMySQLError(err error) error {
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return err
	}
	sqlState := string(mysqlErr.SQLState[:])
	sqlStateClass := sqlState[:2]
	if sqlStateClass == "22" {
		return fmt.Errorf("%w: %w", core.ErrInvalidEvent, err)
	}
	switch mysqlErr.Number {
	case 1048, 1264, 1265, 1292, 1366, 1406, 1452, 3140, 3141, 3819, 4025:
		return fmt.Errorf("%w: %w", core.ErrInvalidEvent, err)
	}
	return err
}

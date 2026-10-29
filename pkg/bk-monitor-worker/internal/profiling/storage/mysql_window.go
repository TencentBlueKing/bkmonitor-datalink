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
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	selectDueHostExecutionIDsSQL = `SELECT execution_id
FROM apm_activeprofilinghostexecution
WHERE status = ? AND unknown_at IS NOT NULL AND unknown_at <= ?
ORDER BY unknown_at`
)

// MarkUnknown changes expired running hosts to unknown and refreshes execution
// summaries. The page limit counts executions rather than hosts so one large
// execution cannot consume the entire scan batch.
func (s *MySQLStore) MarkUnknown(ctx context.Context, nowMS int64, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("unknown scan limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx, selectDueHostExecutionIDsSQL, core.StatusRunning, nowMS)
	if err != nil {
		return 0, fmt.Errorf("find executions with due unknown hosts: %w", err)
	}
	var executionIDs []int
	seenExecutionIDs := make(map[int]struct{}, limit)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan due execution id: %w", err)
		}
		if _, exists := seenExecutionIDs[id]; exists {
			continue
		}
		seenExecutionIDs[id] = struct{}{}
		executionIDs = append(executionIDs, id)
		if len(executionIDs) == limit {
			break
		}
	}
	rowsErr := rows.Err()
	if err := rows.Close(); err != nil && rowsErr == nil {
		rowsErr = err
	}
	if rowsErr != nil {
		return 0, fmt.Errorf("read due execution ids: %w", rowsErr)
	}

	changed := 0
	for _, executionID := range executionIDs {
		n, err := s.markExecutionUnknown(ctx, executionID, nowMS)
		changed += n
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func (s *MySQLStore) markExecutionUnknown(ctx context.Context, executionID int, nowMS int64) (int, error) {
	started := time.Now()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire unknown transition connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := acquireExecutionLock(ctx, conn, executionID); err != nil {
		return 0, err
	}
	defer releaseExecutionLock(conn, executionID)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin unknown transition transaction execution_id=%d: %w", executionID, err)
	}
	defer func() { _ = tx.Rollback() }()

	execution, err := scanExecution(tx.QueryRowContext(ctx, selectExecutionByIDLockSQL, executionID))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read execution for unknown transition id=%d: %w", executionID, err)
	}
	now := time.Now()
	result, err := tx.ExecContext(ctx, `UPDATE apm_activeprofilinghostexecution
		SET status = ?, updated_at = ?
		WHERE execution_id = ? AND status = ? AND unknown_at IS NOT NULL AND unknown_at <= ?`,
		core.StatusUnknown, now, executionID, core.StatusRunning, nowMS)
	if err != nil {
		return 0, fmt.Errorf("mark due hosts unknown execution_id=%d: %w", executionID, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read unknown transition row count execution_id=%d: %w", executionID, err)
	}
	if changed == 0 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit unchanged unknown transition execution_id=%d: %w", executionID, err)
		}
		return 0, nil
	}
	hostStates, err := loadExecutionHosts(ctx, tx, executionID)
	if err != nil {
		return 0, err
	}
	aggregate, err := aggregateExecution(execution, hostStates)
	if err != nil {
		return 0, err
	}
	if executionAggregateChanged(execution, aggregate) {
		if _, err := tx.ExecContext(ctx, updateExecutionSummarySQL,
			aggregate.status, aggregate.statusSummary, aggregate.targetCount,
			aggregate.startedAt, nullableInt(aggregate.finishedAt), now, executionID,
		); err != nil {
			return 0, fmt.Errorf("update active profiling summary id=%d: %w", executionID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit unknown transition execution_id=%d: %w", executionID, err)
	}
	logger.Debugf("[profiling] windows closed execution_id=%d hosts=%d execution_status=%s summary=%s cost=%s",
		executionID, changed, aggregate.status, aggregate.statusSummary, time.Since(started))
	return int(changed), nil
}

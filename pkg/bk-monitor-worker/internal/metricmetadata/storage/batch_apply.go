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
	"sort"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Apply reads current report-scope members under the group lock, then commits
// members, counts, and report-value reference deltas together.
func (s *MySQLStore) Apply(ctx context.Context, groupID int, records []core.Record, now time.Time) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	inputRecords := len(records)
	for attempt := 0; attempt < 3; attempt++ {
		uniqueRecords := core.KeepLast(records)
		attemptStarted := time.Now()
		result, err := s.apply(ctx, groupID, uniqueRecords, now)
		attemptDuration := time.Since(attemptStarted)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("apply metric metadata group_id=%d records=%d timeout=%s: %w", groupID, len(records), s.timeout, ctx.Err())
		}
		var me *mysqlDriver.MySQLError
		retry := errors.Is(err, errConflict) || (errors.As(err, &me) && (me.Number == 1062 || me.Number == 1213 || me.Number == 1205))
		if !retry || attempt == 2 {
			return Result{}, fmt.Errorf("apply metric metadata group %d: %w", groupID, err)
		}
		logger.Debugf("metric metadata MySQL apply retrying group_id=%d input_records=%d unique_records=%d attempt=%d duration=%s err=%v", groupID, inputRecords, len(uniqueRecords), attempt+1, attemptDuration, err)
		s.InvalidateReportValueCache(groupID)
	}
	return Result{}, errConflict
}

type reportValue struct {
	id               int64
	field, fp        string
	reportScopeCount int
}
type reportValueKey struct{ field, fp string }
type reportScopeRow struct {
	state          core.ScopeState
	reportValueIDs []uint32
}

func lockGroup(ctx context.Context, tx *sql.Tx, groupID int) error {
	// All writers and expiration for a group take this row first. It serializes
	// capacity checks and establishes the shared lock order.
	var id int
	err := tx.QueryRowContext(ctx, `SELECT time_series_group_id FROM metadata_timeseriesgroup
WHERE time_series_group_id=? AND is_enable=1 AND is_delete=0 FOR UPDATE`, groupID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrGroupUnavailable
	}
	return err
}

func (s *MySQLStore) apply(ctx context.Context, groupID int, records []core.Record, now time.Time) (Result, error) {
	started := time.Now()
	batch := &applyBatch{
		store: s, ctx: ctx, groupID: groupID, now: now, records: records,
		result: Result{Dropped: make(map[string]int)}, dbScopeTotal: -1,
	}
	outcome := "error"
	defer func() { batch.logTiming(started, outcome) }()
	if len(records) == 0 {
		outcome = "empty"
		return batch.result, nil
	}

	stageStarted := time.Now()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	batch.timing.beginTx = time.Since(stageStarted)
	if err != nil {
		return batch.result, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			logger.Warnf("metric metadata transaction rollback failed group_id=%d err=%v", groupID, rollbackErr)
		}
	}()
	batch.tx = tx

	stageStarted = time.Now()
	if err = lockGroup(ctx, tx, groupID); err != nil {
		batch.timing.groupLock = time.Since(stageStarted)
		return batch.result, err
	}
	batch.timing.groupLock = time.Since(stageStarted)

	if err = batch.loadScopes(); err != nil {
		return batch.result, err
	}
	if err = batch.loadScopeValues(); err != nil {
		return batch.result, err
	}
	if err = batch.loadInputValues(); err != nil {
		return batch.result, err
	}
	keys, err := batch.admitRecords()
	if err != nil {
		return batch.result, err
	}
	if err = batch.insertValues(keys); err != nil {
		return batch.result, err
	}
	delta, err := batch.writeScopes()
	if err != nil {
		return batch.result, err
	}
	if err = batch.updateReferences(delta); err != nil {
		return batch.result, err
	}

	stageStarted = time.Now()
	if err = tx.Commit(); err != nil {
		batch.timing.commit = time.Since(stageStarted)
		return Result{}, err
	}
	batch.timing.commit = time.Since(stageStarted)
	batch.publishCache()
	outcome = "success"
	return batch.result, nil
}

func readReportValuesByKeys(ctx context.Context, tx *sql.Tx, groupID int, keys []reportValueKey) ([]reportValue, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	reportValues := make([]reportValue, 0, len(keys))
	for start := 0; start < len(keys); start += sqlBatchSize {
		end := min(start+sqlBatchSize, len(keys))
		chunk := keys[start:end]
		var query strings.Builder
		query.WriteString(`SELECT id,field_name,fingerprint,scope_count FROM metadata_timeseriesreportvalue WHERE group_id=? AND (field_name,fingerprint) IN (`)
		args := make([]any, 1, 1+len(chunk)*2)
		args[0] = groupID
		for i, key := range chunk {
			if i > 0 {
				query.WriteByte(',')
			}
			query.WriteString("(?,?)")
			args = append(args, key.field, key.fp)
		}
		query.WriteByte(')')
		rows, err := tx.QueryContext(ctx, query.String(), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var storedReportValue reportValue
			if err = rows.Scan(&storedReportValue.id, &storedReportValue.field, &storedReportValue.fp, &storedReportValue.reportScopeCount); err != nil {
				break
			}
			reportValues = append(reportValues, storedReportValue)
		}
		if err == nil {
			err = rows.Err()
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
	}
	return reportValues, nil
}

func loadReportReferenceValues(ctx context.Context, tx *sql.Tx, groupID int, delta map[uint32]int, cached map[uint32]reportValue) (map[uint32]reportValue, error) {
	reportValueIDs := make([]uint32, 0, len(delta))
	for id, change := range delta {
		if change != 0 {
			reportValueIDs = append(reportValueIDs, id)
		}
	}
	sort.Slice(reportValueIDs, func(i, j int) bool { return reportValueIDs[i] < reportValueIDs[j] })
	reportValues := make(map[uint32]reportValue, len(reportValueIDs))
	for start := 0; start < len(reportValueIDs); start += sqlBatchSize {
		chunk := reportValueIDs[start:min(start+sqlBatchSize, len(reportValueIDs))]
		args := make([]any, 1, len(chunk)+1)
		args[0] = groupID
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,field_name,fingerprint,scope_count FROM metadata_timeseriesreportvalue WHERE group_id=? AND id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`) ORDER BY id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var storedReportValue reportValue
			if err = rows.Scan(&storedReportValue.id, &storedReportValue.field, &storedReportValue.fp, &storedReportValue.reportScopeCount); err != nil {
				break
			}
			id := uint32(storedReportValue.id)
			cachedValue, ok := cached[id]
			if !ok || cachedValue.field != storedReportValue.field || cachedValue.fp != storedReportValue.fp {
				err = errConflict
				break
			}
			reportValues[id] = storedReportValue
		}
		if err == nil {
			err = rows.Err()
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		if len(reportValues) < start+len(chunk) {
			return nil, errConflict
		}
	}
	return reportValues, nil
}

func bumpReferences(ctx context.Context, tx *sql.Tx, groupID int, delta map[uint32]int, old map[uint32]reportValue) error {
	// Validate underflow and update IDs in ascending order for a consistent write order.
	reportValueIDs := make([]uint32, 0, len(delta))
	for id, d := range delta {
		if d != 0 {
			if reportValue, ok := old[id]; ok && reportValue.reportScopeCount+d < 0 {
				return fmt.Errorf("reference count underflow for value %d", id)
			}
			reportValueIDs = append(reportValueIDs, id)
		}
	}
	sort.Slice(reportValueIDs, func(i, j int) bool { return reportValueIDs[i] < reportValueIDs[j] })
	for start := 0; start < len(reportValueIDs); start += sqlBatchSize {
		chunk := reportValueIDs[start:min(start+sqlBatchSize, len(reportValueIDs))]
		var q strings.Builder
		args := make([]any, 0, len(chunk)*3+1)
		q.WriteString(`UPDATE metadata_timeseriesreportvalue SET scope_count=scope_count+CASE id `)
		for _, id := range chunk {
			q.WriteString("WHEN ? THEN ? ")
			args = append(args, id, delta[id])
		}
		q.WriteString("END WHERE group_id=? AND id IN (")
		args = append(args, groupID)
		for i, id := range chunk {
			if i > 0 {
				q.WriteByte(',')
			}
			q.WriteByte('?')
			args = append(args, id)
		}
		q.WriteByte(')')
		res, err := tx.ExecContext(ctx, q.String(), args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != int64(len(chunk)) {
			return errConflict
		}
	}
	return nil
}

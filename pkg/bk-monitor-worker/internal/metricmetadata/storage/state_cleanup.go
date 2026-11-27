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
	"math"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Expire removes at most 500 stale report scopes per call, preserving report-value reference counts.
func (s *MySQLStore) Expire(ctx context.Context, cutoff time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM metadata_timeseriesreportscope WHERE last_seen<?
	UNION SELECT group_id FROM metadata_timeseriesreportvalue WHERE scope_count=0
	ORDER BY group_id LIMIT 100`, cutoff)
	if err != nil {
		return 0, err
	}
	// Discover a bounded set of candidate groups; expireGroup rechecks stale rows
	// under locks before deleting them.
	var groups []int
	for rows.Next() {
		var group int
		if err = rows.Scan(&group); err != nil {
			break
		}
		groups = append(groups, group)
	}
	if err == nil {
		err = rows.Err()
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, group := range groups {
		if removed >= 500 {
			break
		}
		n, e := s.expireGroup(ctx, group, cutoff, 500-removed)
		if e != nil {
			return removed, e
		}
		removed += n
	}
	return removed, nil
}

// PurgeMetricReferences 在一个分组事务内移除已删除指标的所有 scope 引用。
func (s *MySQLStore) PurgeMetricReferences(ctx context.Context, groupID int, fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			logger.Warnf("metric metadata purge rollback failed group_id=%d err=%v", groupID, rollbackErr)
		}
	}()
	var lockedGroup int
	if err := tx.QueryRowContext(ctx, `SELECT time_series_group_id FROM metadata_timeseriesgroup WHERE time_series_group_id=? FOR UPDATE`, groupID).Scan(&lockedGroup); err != nil {
		return err
	}
	reportScopeRows, err := tx.QueryContext(ctx, `SELECT id,report_value_ids,value_count FROM metadata_timeseriesreportscope WHERE group_id=? ORDER BY id`, groupID)
	if err != nil {
		return err
	}
	type reportScopeMembers struct {
		id             int64
		reportValueIDs []uint32
	}
	var reportScopes []reportScopeMembers
	for reportScopeRows.Next() {
		var reportScopeEntry reportScopeMembers
		var blob []byte
		var reportValueCount int
		if err = reportScopeRows.Scan(&reportScopeEntry.id, &blob, &reportValueCount); err != nil {
			break
		}
		reportScopeEntry.reportValueIDs, err = core.DecodeIDs(blob, reportValueCount)
		if err != nil {
			break
		}
		reportScopes = append(reportScopes, reportScopeEntry)
	}
	if err == nil {
		err = reportScopeRows.Err()
	}
	if closeErr := reportScopeRows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	fieldSet := make(map[string]struct{}, len(fields))
	args := make([]any, 1, len(fields)+1)
	args[0] = groupID
	for _, field := range fields {
		fieldSet[field] = struct{}{}
		args = append(args, field)
	}
	reportValueRows, err := tx.QueryContext(ctx, `SELECT id,field_name,scope_count FROM metadata_timeseriesreportvalue WHERE group_id=? AND field_name IN (`+strings.TrimSuffix(strings.Repeat("?,", len(fields)), ",")+`) ORDER BY id`, args...)
	if err != nil {
		return err
	}
	targetReportValueIDs := make(map[uint32]struct{})
	knownReportValues := make(map[uint32]reportValue)
	for reportValueRows.Next() {
		var id int64
		var field string
		var reportScopeCount int
		if err = reportValueRows.Scan(&id, &field, &reportScopeCount); err != nil {
			break
		}
		if id <= 0 || id > math.MaxUint32 {
			err = fmt.Errorf("invalid persisted report value ID")
			break
		}
		if _, ok := fieldSet[field]; ok {
			storedReportValue := reportValue{id: id, field: field, reportScopeCount: reportScopeCount}
			targetReportValueIDs[uint32(id)] = struct{}{}
			knownReportValues[uint32(id)] = storedReportValue
		}
	}
	if err == nil {
		err = reportValueRows.Err()
	}
	if closeErr := reportValueRows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if len(targetReportValueIDs) == 0 {
		return tx.Commit()
	}
	deletedReportValueDeltas := make(map[uint32]int)
	for _, reportScopeEntry := range reportScopes {
		kept := reportScopeEntry.reportValueIDs[:0]
		changed := false
		for _, id := range reportScopeEntry.reportValueIDs {
			if _, ok := targetReportValueIDs[id]; ok {
				deletedReportValueDeltas[id]--
				changed = true
			} else {
				kept = append(kept, id)
			}
		}
		if !changed {
			continue
		}
		encoded, encodeErr := core.EncodeIDs(kept)
		if encodeErr != nil {
			return encodeErr
		}
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_timeseriesreportscope SET report_value_ids=?,value_count=?,version=version+1 WHERE group_id=? AND id=?`, encoded, len(kept), groupID, reportScopeEntry.id); err != nil {
			return err
		}
	}
	referenceReportValues, err := loadReportReferenceValues(ctx, tx, groupID, deletedReportValueDeltas, knownReportValues)
	if err != nil {
		return err
	}
	if err = bumpReferences(ctx, tx, groupID, deletedReportValueDeltas, referenceReportValues); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_timeseriesreportvalue WHERE group_id=? AND field_name IN (`+strings.TrimSuffix(strings.Repeat("?,", len(fields)), ",")+`) AND scope_count=0`, args...); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.InvalidateReportValueCache(groupID)
	return nil
}

func (s *MySQLStore) expireGroup(ctx context.Context, groupID int, cutoff time.Time, limit int) (int, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			logger.Warnf("metric metadata expiry rollback failed group_id=%d err=%v", groupID, rollbackErr)
		}
	}()
	// Match the writer lock order: group, stale scopes, then referenced values.
	var lockedID int
	err = tx.QueryRowContext(ctx, `SELECT time_series_group_id FROM metadata_timeseriesgroup WHERE time_series_group_id=? FOR UPDATE`, groupID).Scan(&lockedID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,report_value_ids,value_count FROM metadata_timeseriesreportscope WHERE group_id=? AND last_seen<? ORDER BY id LIMIT ?`, groupID, cutoff, limit)
	if err != nil {
		return 0, err
	}
	var reportScopeIDs []int64
	delta := make(map[uint32]int)
	for rows.Next() {
		var id int64
		var b []byte
		var reportValueCount int
		if err = rows.Scan(&id, &b, &reportValueCount); err != nil {
			break
		}
		reportValueIDs, e := core.DecodeIDs(b, reportValueCount)
		if e != nil {
			err = e
			break
		}
		reportScopeIDs = append(reportScopeIDs, id)
		for _, reportValueID := range reportValueIDs {
			delta[reportValueID]--
		}
	}
	if err == nil {
		err = rows.Err()
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	reportValueIDSet := make(map[uint32]struct{}, len(delta))
	for id, change := range delta {
		if change != 0 {
			reportValueIDSet[id] = struct{}{}
		}
	}
	reportValueMembers, err := loadReportValuesByIDs(ctx, tx, reportValueIDSet)
	if err != nil {
		return 0, err
	}
	old := make(map[uint32]reportValue, len(reportValueMembers))
	for id := range reportValueIDSet {
		member, ok := reportValueMembers[id]
		if !ok {
			return 0, fmt.Errorf("expired report scope references missing report value %d", id)
		}
		if member.group != groupID {
			return 0, fmt.Errorf("report value %d belongs to group %d, expected %d", id, member.group, groupID)
		}
		old[id] = reportValue{id: int64(id), field: member.field, fp: member.fp, reportScopeCount: member.reportScopeCount}
	}
	referenceReportValues, err := loadReportReferenceValues(ctx, tx, groupID, delta, old)
	if err != nil {
		return 0, err
	}
	if err = bumpReferences(ctx, tx, groupID, delta, referenceReportValues); err != nil {
		return 0, err
	}
	for _, id := range reportScopeIDs {
		if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_timeseriesreportscope WHERE group_id=? AND id=?`, groupID, id); err != nil {
			return 0, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_timeseriesreportvalue WHERE group_id=? AND scope_count=0 LIMIT 500`, groupID); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	s.InvalidateReportValueCache(groupID)
	return len(reportScopeIDs), nil
}

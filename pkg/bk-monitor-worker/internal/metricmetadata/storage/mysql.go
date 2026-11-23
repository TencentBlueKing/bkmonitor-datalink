// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

// Package storage owns Metadata schema access and the transactions that keep
// report-scope membership and report-value reference counts consistent.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const sqlBatchSize = 1000

var errConflict = errors.New("metadata state changed during transaction")

// IsTimeout reports database and network timeouts that the run loop can retry.
func IsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1205
}

// ErrGroupUnavailable means a report's cached route no longer names an enabled group.
var ErrGroupUnavailable = errors.New("metric metadata group is unavailable")

type reportValueMember struct {
	group            int
	field            string
	fp               string
	reportScopeCount int
}

// MetricRoute keeps only the group fields needed to route and validate reports.
type MetricRoute struct {
	GroupID  uint
	TenantID string
	BizID    int
}

// Result contains only snapshots confirmed by a successful database commit.
type Result struct {
	Scopes           []core.ScopeState
	CreatedValues    int
	ChangedScopes    int
	CountChanges     int
	DuplicateRecords int
	Dropped          map[string]int
}

// MySQLStore atomically updates report-scope members and report-value reference counts.
type MySQLStore struct {
	db                 *sql.DB
	timeout            time.Duration
	maxReportScopes    int
	maxReportValues    int
	touchInterval      time.Duration
	loadPageSize       int
	cacheMu            sync.Mutex
	reportValueIDCache map[int]map[reportValueKey]uint32
}

// NewMySQLStore uses a caller-owned connection pool. A zero capacity limit is unlimited.
func NewMySQLStore(db *sql.DB, timeout time.Duration, maxReportScopes, maxReportValues int, touchInterval time.Duration, loadPageSize int) *MySQLStore {
	return &MySQLStore{db: db, timeout: timeout, maxReportScopes: maxReportScopes, maxReportValues: maxReportValues, touchInterval: touchInterval, loadPageSize: loadPageSize, reportValueIDCache: make(map[int]map[reportValueKey]uint32)}
}

// InvalidateReportValueCache drops cached report-value IDs after report rows change elsewhere.
func (s *MySQLStore) InvalidateReportValueCache(groupID int) {
	s.cacheMu.Lock()
	delete(s.reportValueIDCache, groupID)
	s.cacheMu.Unlock()
}

// CheckSchema fails before consuming if the externally managed migrations are absent.
func (s *MySQLStore) CheckSchema(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	for _, q := range []string{
		`SELECT id,group_id,field_name,fingerprint,original_name,original_type,metric_type,meta,scope_count FROM metadata_timeseriesreportvalue LIMIT 0`,
		`SELECT id,group_id,scope_hash,scope_type,labels,raw_scope,report_value_ids,value_count,last_seen,version FROM metadata_timeseriesreportscope LIMIT 0`,
	} {
		rows, err := s.db.QueryContext(ctx, q)
		if err != nil {
			return fmt.Errorf("metric metadata schema is unavailable; apply Metadata migrations first: %w", err)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// LoadMetricRoutes reads one process-wide snapshot of enabled DataID routes.
func (s *MySQLStore) LoadMetricRoutes(ctx context.Context) (map[uint]MetricRoute, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT bk_data_id,time_series_group_id,bk_tenant_id,bk_biz_id
	FROM metadata_timeseriesgroup WHERE is_enable=1 AND is_delete=0`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			logger.Warnf("metric metadata route rows close failed err=%v", closeErr)
		}
	}()
	routes := make(map[uint]MetricRoute)
	for rows.Next() {
		var dataID uint
		var route MetricRoute
		var tenantID sql.NullString
		if err := rows.Scan(&dataID, &route.GroupID, &tenantID, &route.BizID); err != nil {
			return nil, err
		}
		route.TenantID = tenantID.String
		if old, ok := routes[dataID]; ok && old.GroupID != route.GroupID {
			return nil, fmt.Errorf("ambiguous metadata route for dataid %d", dataID)
		}
		routes[dataID] = route
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return routes, nil
}

// GroupForDataID 按 Kafka dimension.dataid（对应 bk_data_id）查询启用分组。
func (s *MySQLStore) GroupForDataID(ctx context.Context, dataID uint) (MetricRoute, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	const query = `SELECT time_series_group_id,bk_tenant_id,bk_biz_id
	FROM metadata_timeseriesgroup WHERE is_enable=1 AND is_delete=0 AND bk_data_id=?`
	rows, err := s.db.QueryContext(ctx, query, dataID)
	if err != nil {
		return MetricRoute{}, false, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			logger.Warnf("metric metadata dataid route rows close failed dataid=%d err=%v", dataID, closeErr)
		}
	}()
	var route MetricRoute
	found := false
	for rows.Next() {
		var candidate MetricRoute
		var tenantID sql.NullString
		if err := rows.Scan(&candidate.GroupID, &tenantID, &candidate.BizID); err != nil {
			return MetricRoute{}, false, err
		}
		candidate.TenantID = tenantID.String
		if found {
			return MetricRoute{}, false, fmt.Errorf("ambiguous metadata route for dataid %d", dataID)
		}
		route, found = candidate, true
	}
	if err := rows.Err(); err != nil {
		return MetricRoute{}, false, err
	}
	return route, found, nil
}

// LoadAll reads one consistent snapshot of every persisted report scope and value.
// Rows are paged to bound query memory while the transaction preserves one snapshot.
func (s *MySQLStore) LoadAll(ctx context.Context) (map[core.ScopeKey]core.ScopeState, error) {
	started := time.Now()
	pages, valueQueryBatches, valueIDs, scopes, members := 0, 0, 0, 0, 0
	defer func() {
		logger.Debugf("metric metadata state load timing duration=%s pages=%d value_query_batches_planned=%d value_ids=%d scopes=%d members=%d", time.Since(started), pages, valueQueryBatches, valueIDs, scopes, members)
	}()
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			logger.Warnf("metric metadata state snapshot rollback failed err=%v", rollbackErr)
		}
	}()
	states := make(map[core.ScopeKey]core.ScopeState)
	lastID := int64(0)
	for {
		rows, queryErr := tx.QueryContext(ctx, `SELECT id,group_id,scope_hash,report_value_ids,value_count,last_seen,version
		FROM metadata_timeseriesreportscope WHERE id>? ORDER BY id LIMIT ?`, lastID, s.loadPageSize)
		if queryErr != nil {
			return nil, queryErr
		}
		type reportScopeRow struct {
			state          core.ScopeState
			reportValueIDs []uint32
		}
		pages++
		page := make([]reportScopeRow, 0, s.loadPageSize)
		reportValueIDs := make(map[uint32]struct{})
		for rows.Next() {
			var row reportScopeRow
			var blob []byte
			var reportValueCount int
			if err = rows.Scan(&row.state.ID, &row.state.Key.GroupID, &row.state.Key.Hash, &blob, &reportValueCount, &row.state.LastSeen, &row.state.Version); err != nil {
				break
			}
			if row.state.ID <= lastID {
				err = fmt.Errorf("scope IDs are not strictly increasing")
				break
			}
			lastID = row.state.ID
			row.reportValueIDs, err = core.DecodeIDs(blob, reportValueCount)
			if err != nil {
				err = fmt.Errorf("scope %d: %w", row.state.ID, err)
				break
			}
			for _, id := range row.reportValueIDs {
				reportValueIDs[id] = struct{}{}
			}
			page = append(page, row)
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
		valueIDs += len(reportValueIDs)
		valueQueryBatches += (len(reportValueIDs) + sqlBatchSize - 1) / sqlBatchSize
		reportValuesByID, err := loadReportValuesByIDs(ctx, tx, reportValueIDs)
		if err != nil {
			return nil, err
		}
		for _, row := range page {
			row.state.Metrics = make(map[string]core.MetricState, len(row.reportValueIDs))
			for _, id := range row.reportValueIDs {
				reportValue, ok := reportValuesByID[id]
				if !ok || reportValue.group != row.state.Key.GroupID {
					return nil, fmt.Errorf("missing value %d in scope %d", id, row.state.ID)
				}
				if _, exists := row.state.Metrics[reportValue.field]; exists {
					return nil, fmt.Errorf("duplicate metric in persisted scope %d", row.state.ID)
				}
				row.state.Metrics[reportValue.field] = core.MetricState{ValueID: id, Fingerprint: reportValue.fp}
			}
			states[row.state.Key] = row.state
			scopes++
			members += len(row.state.Metrics)
		}
		if len(page) < s.loadPageSize {
			break
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return states, nil
}

func loadReportValuesByIDs(ctx context.Context, tx *sql.Tx, reportValueIDs map[uint32]struct{}) (map[uint32]reportValueMember, error) {
	reportValues := make(map[uint32]reportValueMember, len(reportValueIDs))
	ordered := make([]uint32, 0, len(reportValueIDs))
	for id := range reportValueIDs {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for start := 0; start < len(ordered); start += sqlBatchSize {
		end := min(start+sqlBatchSize, len(ordered))
		chunk := ordered[start:end]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		query := `SELECT id,group_id,field_name,fingerprint,scope_count FROM metadata_timeseriesreportvalue WHERE id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",") + `)`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var reportValue reportValueMember
			if err = rows.Scan(&id, &reportValue.group, &reportValue.field, &reportValue.fp, &reportValue.reportScopeCount); err != nil {
				break
			}
			if id <= 0 || id > math.MaxUint32 {
				err = fmt.Errorf("persisted value ID outside uint32 range")
				break
			}
			reportValues[uint32(id)] = reportValue
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

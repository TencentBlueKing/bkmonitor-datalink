// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// AppendAlertLog 委托 batch-of-one，保证单条与批量追加使用相同语义。
func (r *Repository) AppendAlertLog(
	ctx context.Context,
	log domain.AlertLog,
) (store.AppendAlertLogResult, error) {
	items, err := r.AppendAlertLogs(ctx, []domain.AlertLog{log})
	if err != nil {
		return store.AppendAlertLogResult{}, err
	}
	if len(items) != 1 {
		return store.AppendAlertLogResult{}, fmt.Errorf("append alert log: batch writer returned %d items", len(items))
	}
	return items[0].Result, items[0].Err
}

// AppendAlertLogs 按输入顺序逐项追加不可变流水，不承诺整批事务或多行 SQL。
func (r *Repository) AppendAlertLogs(
	ctx context.Context,
	logs []domain.AlertLog,
) ([]store.AppendAlertLogItemResult, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if len(logs) == 0 || len(logs) > store.MaxBatchSize {
		return nil, fmt.Errorf(
			"%w: alert log batch size must be between 1 and %d",
			store.ErrInvalidArgument,
			store.MaxBatchSize,
		)
	}
	results := make([]store.AppendAlertLogItemResult, len(logs))
	for index, log := range logs {
		results[index].Result, results[index].Err = r.appendAlertLog(ctx, log)
	}
	return results, nil
}

func (r *Repository) appendAlertLog(
	ctx context.Context,
	log domain.AlertLog,
) (store.AppendAlertLogResult, error) {
	if err := contextError(ctx); err != nil {
		return store.AppendAlertLogResult{}, err
	}
	normalized, err := log.Normalize()
	if err != nil {
		return store.AppendAlertLogResult{}, fmt.Errorf("%w: normalize alert log: %w", store.ErrInvalidArgument, err)
	}
	if err := validateIdentity(normalized.BKTenantID, "log_id", normalized.LogID); err != nil {
		return store.AppendAlertLogResult{}, err
	}
	if err := validateIdentityPart("alert_id", normalized.AlertID); err != nil {
		return store.AppendAlertLogResult{}, err
	}
	payload, err := encodeAlertLog(normalized)
	if err != nil {
		return store.AppendAlertLogResult{}, err
	}
	_, err = r.db.ExecContext(
		ctx,
		`INSERT INTO linkd_alert_logs
            (bk_tenant_id, log_id, alert_id, created_time_ns, payload)
         VALUES (?, ?, ?, ?, ?)`,
		normalized.BKTenantID,
		normalized.LogID,
		normalized.AlertID,
		normalized.CreatedTime.UnixNano(),
		payload,
	)
	if err == nil {
		return store.AppendAlertLogResult{Log: normalized.Clone(), Created: true}, nil
	}
	if !isDuplicateKey(err) {
		return store.AppendAlertLogResult{}, fmt.Errorf("insert alert log %q: %w", normalized.LogID, err)
	}
	existing, getErr := r.getAlertLog(ctx, normalized.BKTenantID, normalized.LogID)
	if getErr != nil {
		return store.AppendAlertLogResult{}, fmt.Errorf("read duplicate alert log %q: %w", normalized.LogID, getErr)
	}
	if !reflect.DeepEqual(existing, normalized) {
		return store.AppendAlertLogResult{}, fmt.Errorf(
			"%w: alert log %q already contains different content",
			store.ErrIdentityConflict,
			normalized.LogID,
		)
	}
	return store.AppendAlertLogResult{Log: existing.Clone(), Created: false}, nil
}

// ListAlertLogs 按 created_time、log_id 稳定读取指定 Alert 的流水。
func (r *Repository) ListAlertLogs(
	ctx context.Context,
	bkTenantID, alertID string,
	page store.PageRequest,
) (store.AlertLogPage, error) {
	if err := contextError(ctx); err != nil {
		return store.AlertLogPage{}, err
	}
	if err := validateIdentity(bkTenantID, "alert_id", alertID); err != nil {
		return store.AlertLogPage{}, err
	}
	normalizedPage, err := page.Normalize()
	if err != nil {
		return store.AlertLogPage{}, err
	}
	expectedCursor := cursorPayload{
		Kind:     cursorKindAlertLog,
		TenantID: bkTenantID,
		ParentID: alertID,
	}
	var cursor cursorPayload
	if normalizedPage.Cursor != "" {
		cursor, err = decodeCursor(normalizedPage.Cursor, expectedCursor)
		if err != nil {
			return store.AlertLogPage{}, err
		}
	}
	query := `SELECT payload, created_time_ns, log_id
		        FROM linkd_alert_logs
               WHERE bk_tenant_id = ? AND alert_id = ?`
	arguments := []any{bkTenantID, alertID}
	if normalizedPage.Cursor != "" {
		query += ` AND (created_time_ns > ? OR (created_time_ns = ? AND log_id > ?))`
		arguments = append(arguments, cursor.TimeNS, cursor.TimeNS, cursor.ObjectID)
	}
	query += ` ORDER BY created_time_ns, log_id LIMIT ?`
	arguments = append(arguments, normalizedPage.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return store.AlertLogPage{}, fmt.Errorf("list alert logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type scannedLog struct {
		log       domain.AlertLog
		createdNS int64
		logID     string
	}
	candidates := make([]scannedLog, 0, normalizedPage.Limit+1)
	for rows.Next() {
		var payload []byte
		var createdNS int64
		var logID string
		if err := rows.Scan(&payload, &createdNS, &logID); err != nil {
			return store.AlertLogPage{}, fmt.Errorf("scan alert log row: %w", err)
		}
		decoded, err := decodeAlertLog(payload)
		if err != nil {
			return store.AlertLogPage{}, err
		}
		candidates = append(candidates, scannedLog{log: decoded, createdNS: createdNS, logID: logID})
	}
	if err := rows.Err(); err != nil {
		return store.AlertLogPage{}, fmt.Errorf("iterate alert logs: %w", err)
	}
	count := min(len(candidates), normalizedPage.Limit)
	result := store.AlertLogPage{Logs: make([]domain.AlertLog, 0, count)}
	for _, candidate := range candidates[:count] {
		result.Logs = append(result.Logs, candidate.log)
	}
	if len(candidates) > normalizedPage.Limit {
		last := candidates[normalizedPage.Limit-1]
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind:     cursorKindAlertLog,
			TenantID: bkTenantID,
			ParentID: alertID,
			TimeNS:   last.createdNS,
			ObjectID: last.logID,
		})
		if err != nil {
			return store.AlertLogPage{}, err
		}
	}
	return result, nil
}

func (r *Repository) getAlertLog(
	ctx context.Context,
	bkTenantID, logID string,
) (domain.AlertLog, error) {
	var payload []byte
	err := r.db.QueryRowContext(
		ctx,
		`SELECT payload
	       FROM linkd_alert_logs
          WHERE bk_tenant_id = ? AND log_id = ?`,
		bkTenantID,
		logID,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AlertLog{}, fmt.Errorf("%w: alert log %q", store.ErrNotFound, logID)
	}
	if err != nil {
		return domain.AlertLog{}, fmt.Errorf("read alert log %q: %w", logID, err)
	}
	return decodeAlertLog(payload)
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
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

const storedAlertColumns = `payload, version`

func (r *Repository) CreateAlert(ctx context.Context, alert domain.Alert) (store.CreateAlertResult, error) {
	if err := contextError(ctx); err != nil {
		return store.CreateAlertResult{}, err
	}
	normalized, err := alert.Normalize()
	if err != nil {
		return store.CreateAlertResult{}, fmt.Errorf("%w: normalize alert: %w", store.ErrInvalidArgument, err)
	}
	if normalized.Status != domain.AlertStatusActive {
		return store.CreateAlertResult{}, fmt.Errorf("%w: new alert must be active", store.ErrInvalidArgument)
	}
	if err := validateAlertIdentityColumns(normalized); err != nil {
		return store.CreateAlertResult{}, err
	}
	payload, err := encodeAlert(normalized)
	if err != nil {
		return store.CreateAlertResult{}, err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO linkd_alerts (bk_tenant_id,alert_id,version,status,event_source_id,fingerprint,severity,latest_event_id,end_type,end_at_ns,active_marker,payload) VALUES (?,?,1,?,?,?,?,?,NULL,NULL,1,?)`, normalized.BKTenantID, normalized.AlertID, normalized.Status, normalized.EventSourceID, normalized.Fingerprint, normalized.Severity, normalized.LatestEventID, payload)
	if err == nil {
		return store.CreateAlertResult{StoredAlert: store.StoredAlert{Alert: normalized.Clone(), Version: versionToken(1)}, Created: true}, nil
	}
	if !isDuplicateKey(err) {
		return store.CreateAlertResult{}, fmt.Errorf("insert alert %q: %w", normalized.AlertID, err)
	}
	existing, readErr := r.GetAlert(ctx, normalized.BKTenantID, normalized.AlertID)
	if readErr == nil {
		if reflect.DeepEqual(existing.Alert, normalized) {
			return store.CreateAlertResult{StoredAlert: existing, Created: false}, nil
		}
		return store.CreateAlertResult{}, fmt.Errorf("%w: alert %q contains different content", store.ErrIdentityConflict, normalized.AlertID)
	}
	if !errors.Is(readErr, store.ErrNotFound) {
		return store.CreateAlertResult{}, readErr
	}
	return store.CreateAlertResult{}, fmt.Errorf("%w: active alert exists for fingerprint", store.ErrIdentityConflict)
}

func (r *Repository) GetAlert(ctx context.Context, tenantID, alertID string) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateIdentity(tenantID, "alert_id", alertID); err != nil {
		return store.StoredAlert{}, err
	}
	stored, err := scanStoredAlert(r.db.QueryRowContext(ctx, `SELECT `+storedAlertColumns+` FROM linkd_alerts WHERE bk_tenant_id=? AND alert_id=?`, tenantID, alertID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
	}
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("read alert %q: %w", alertID, err)
	}
	return stored, nil
}

func (r *Repository) GetAlerts(ctx context.Context, tenantID string, alertIDs []string) (store.AlertBatch, error) {
	if err := contextError(ctx); err != nil {
		return store.AlertBatch{}, err
	}
	ids, err := normalizeBatchIDs(tenantID, "alert_id", alertIDs)
	if err != nil {
		return store.AlertBatch{}, err
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, tenantID)
	for _, id := range ids {
		args = append(args, id)
	}
	query := `SELECT alert_id, ` + storedAlertColumns + ` FROM linkd_alerts WHERE bk_tenant_id=? AND alert_id IN (` + placeholders(len(ids)) + `)` //nolint:gosec // placeholders are fixed question marks.
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return store.AlertBatch{}, err
	}
	defer func() { _ = rows.Close() }()
	found := make(map[string]store.StoredAlert, len(ids))
	for rows.Next() {
		var id string
		var payload []byte
		var version uint64
		if err := rows.Scan(&id, &payload, &version); err != nil {
			return store.AlertBatch{}, err
		}
		alert, err := decodeAlert(payload)
		if err != nil {
			return store.AlertBatch{}, err
		}
		found[id] = store.StoredAlert{Alert: alert, Version: versionToken(version)}
	}
	if err := rows.Err(); err != nil {
		return store.AlertBatch{}, err
	}
	result := store.AlertBatch{Alerts: []store.StoredAlert{}, NotFound: []string{}}
	for _, id := range ids {
		if item, ok := found[id]; ok {
			result.Alerts = append(result.Alerts, item)
		} else {
			result.NotFound = append(result.NotFound, id)
		}
	}
	return result, nil
}

func (r *Repository) FindActiveAlert(ctx context.Context, key store.ActiveAlertKey) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	for name, value := range map[string]string{"bk_tenant_id": key.BKTenantID, "event_source_id": key.EventSourceID, "fingerprint": key.Fingerprint} {
		if err := validateIdentityPart(name, value); err != nil {
			return store.StoredAlert{}, err
		}
	}
	stored, err := scanStoredAlert(r.db.QueryRowContext(ctx, `SELECT `+storedAlertColumns+` FROM linkd_alerts WHERE bk_tenant_id=? AND event_source_id=? AND fingerprint=? AND active_marker=1`, key.BKTenantID, key.EventSourceID, key.Fingerprint))
	if errors.Is(err, sql.ErrNoRows) {
		return store.StoredAlert{}, fmt.Errorf("%w: active alert", store.ErrNotFound)
	}
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("find active alert: %w", err)
	}
	return stored, nil
}

func (r *Repository) FindAlertEndedByEvent(ctx context.Context, tenantID, eventID string) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateIdentity(tenantID, "event_id", eventID); err != nil {
		return store.StoredAlert{}, err
	}
	stored, err := scanStoredAlert(r.db.QueryRowContext(ctx, `SELECT `+storedAlertColumns+` FROM linkd_alerts WHERE bk_tenant_id=? AND latest_event_id=? AND end_type IN (?,?) LIMIT 2`, tenantID, eventID, domain.AlertEndTypeSource, domain.AlertEndTypeSeverityUpgrade))
	if errors.Is(err, sql.ErrNoRows) {
		return store.StoredAlert{}, fmt.Errorf("%w: alert ended by event %q", store.ErrNotFound, eventID)
	}
	if err != nil {
		return store.StoredAlert{}, err
	}
	return stored, nil
}

func (r *Repository) CompareAndSetAlert(ctx context.Context, tenantID, alertID string, expected store.VersionToken, replacement domain.Alert) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateIdentity(tenantID, "alert_id", alertID); err != nil {
		return store.StoredAlert{}, err
	}
	expectedVersion, ok := parseVersion(expected)
	if !ok {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrVersionConflict, alertID)
	}
	normalized, err := replacement.Normalize()
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("%w: normalize replacement: %w", store.ErrInvalidArgument, err)
	}
	if err := validateAlertIdentityColumns(normalized); err != nil {
		return store.StoredAlert{}, err
	}
	payload, err := encodeAlert(normalized)
	if err != nil {
		return store.StoredAlert{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return store.StoredAlert{}, err
	}
	defer rollback(tx)
	current, err := scanStoredAlert(tx.QueryRowContext(ctx, `SELECT `+storedAlertColumns+` FROM linkd_alerts WHERE bk_tenant_id=? AND alert_id=? FOR UPDATE`, tenantID, alertID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
	}
	if err != nil {
		return store.StoredAlert{}, err
	}
	if current.Version != expected {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrVersionConflict, alertID)
	}
	if err := domain.ValidateAlertReplacement(current.Alert, normalized); err != nil {
		return store.StoredAlert{}, fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
	}
	newVersion := expectedVersion + 1
	var endType, endAt any
	if normalized.EndAt != nil {
		endType = normalized.EndType
		endAt = normalized.EndAt.UnixNano()
	}
	var active any = 1
	if normalized.Status.Terminal() {
		active = nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE linkd_alerts SET payload=?,version=?,status=?,latest_event_id=?,end_type=?,end_at_ns=?,active_marker=? WHERE bk_tenant_id=? AND alert_id=? AND version=?`, payload, newVersion, normalized.Status, normalized.LatestEventID, endType, endAt, active, tenantID, alertID, expectedVersion)
	if err != nil {
		if isDuplicateKey(err) {
			return store.StoredAlert{}, fmt.Errorf("%w: active fingerprint", store.ErrIdentityConflict)
		}
		return store.StoredAlert{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrVersionConflict, alertID)
	}
	if err := tx.Commit(); err != nil {
		return store.StoredAlert{}, err
	}
	return store.StoredAlert{Alert: normalized.Clone(), Version: versionToken(newVersion)}, nil
}

func validateAlertIdentityColumns(alert domain.Alert) error {
	for name, value := range map[string]string{"bk_tenant_id": alert.BKTenantID, "alert_id": alert.AlertID, "event_source_id": alert.EventSourceID, "fingerprint": alert.Fingerprint} {
		if err := validateIdentityPart(name, value); err != nil {
			return err
		}
	}
	return nil
}

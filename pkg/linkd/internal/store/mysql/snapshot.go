// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

var _ store.LifecycleAlertSnapshotStore = (*Repository)(nil)

var _ store.LifecycleEventSnapshotStore = (*Repository)(nil)

func affectedVersion(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return store.ErrVersionConflict
	}
	return nil
}

// CompareAndSetEventEnrichmentFromSnapshot 只写丰富字段，来源载荷保持原样；旧版本不能覆盖已完成结果。
func (r *Repository) CompareAndSetEventEnrichmentFromSnapshot(ctx context.Context, current store.StoredEvent, result domain.EventEnrichment) (store.StoredEvent, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredEvent{}, err
	}
	version, ok := parseVersion(current.Version)
	if !ok {
		return store.StoredEvent{}, store.ErrVersionConflict
	}
	updated, err := store.ApplyEventEnrichmentFromSnapshot(current, result)
	if err != nil {
		return store.StoredEvent{}, err
	}
	payload, err := json.Marshal(updated.Enrich)
	if err != nil {
		return store.StoredEvent{}, err
	}
	// JSON_SET 替换整个丰富值；MERGE_PATCH 会删除丰富内部的合法 null 字段。
	written, err := r.db.ExecContext(ctx, `UPDATE linkd_events SET payload=JSON_SET(payload,'$.enrich',CAST(? AS JSON),'$.enrich_status',?,'$.enriched_at',?,'$.enrich_config_digest',?,'$.enrich_source_version',?),version=version+1 WHERE bk_tenant_id=? AND event_id=? AND version=?`, string(payload), updated.EnrichStatus, updated.EnrichedAt.Format(time.RFC3339Nano), updated.EnrichConfigDigest, updated.EnrichSourceVersion, current.Event.BKTenantID, current.Event.EventID, version)
	if err != nil {
		return store.StoredEvent{}, err
	}
	if err := affectedVersion(written); err != nil {
		return store.StoredEvent{}, err
	}
	return store.StoredEvent{Event: updated, Processing: current.Processing, Version: versionToken(version + 1)}, nil
}

// CompareAndSetLifecycleEventResultFromSnapshot 通过一次条件 UPDATE 保存关联与结果，不重复读取或编码来源载荷。
func (r *Repository) CompareAndSetLifecycleEventResultFromSnapshot(ctx context.Context, current store.StoredEvent, result store.EventResult) (store.StoredEvent, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredEvent{}, err
	}
	version, ok := parseVersion(current.Version)
	if !ok {
		return store.StoredEvent{}, store.ErrVersionConflict
	}
	updated, processing, err := store.ApplyEventResultFromSnapshot(current, result)
	if err != nil {
		return store.StoredEvent{}, err
	}
	payload, err := encodeProcessing(processing)
	if err != nil {
		return store.StoredEvent{}, err
	}
	ids, err := json.Marshal(updated.RelatedAlertIDs)
	if err != nil {
		return store.StoredEvent{}, err
	}
	written, err := r.db.ExecContext(ctx, `UPDATE linkd_events SET payload=JSON_SET(payload,'$.related_alert_ids',CAST(? AS JSON)),processing=?,processing_state=?,related_alert_ids=?,version=version+1 WHERE bk_tenant_id=? AND event_id=? AND version=?`, string(ids), payload, processing.State, string(ids), current.Event.BKTenantID, current.Event.EventID, version)
	if err != nil {
		return store.StoredEvent{}, err
	}
	if err := affectedVersion(written); err != nil {
		return store.StoredEvent{}, err
	}
	return store.StoredEvent{Event: updated, Processing: processing, Version: versionToken(version + 1)}, nil
}

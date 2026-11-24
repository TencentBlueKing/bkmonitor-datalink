// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// batchDocuments 由生产 ES/MySQL 存储提供；单文档测试替身仍可独立运行领域契约。
type batchDocuments interface {
	GetMany(context.Context, string, []string) (map[string]json.RawMessage, error)
	CreateMany(context.Context, string, map[string]json.RawMessage) error
}

const documentBatchSize = 128

func decodeSnapshot(raw json.RawMessage, tenant, id, alert string) (Snapshot, error) {
	var value Snapshot
	if len(raw) > 8<<20 || json.Unmarshal(raw, &value) != nil {
		return Snapshot{}, policy.ErrInvalid
	}
	if value.TenantID != tenant || value.DecisionID != id || value.Alert.BKTenantID != tenant || value.Alert.AlertID != alert {
		return Snapshot{}, policy.ErrAccess
	}
	if err := value.Alert.Validate(); err != nil {
		return Snapshot{}, err
	}
	return value, nil
}

func (j *Journal) snapshots(ctx context.Context, tenant, id string, ids []string) (map[string]Snapshot, error) {
	result := make(map[string]Snapshot, len(ids))
	bytes := 0
	batch, ok := j.docs.(batchDocuments)
	if !ok {
		for _, alert := range ids {
			value, err := j.Snapshot(ctx, tenant, id, alert)
			if errors.Is(err, policy.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			bytes += len(raw)
			if bytes > 32<<20 {
				return nil, policy.ErrUnavailable
			}
			result[alert] = value
		}
		return result, nil
	}
	for start := 0; start < len(ids); start += documentBatchSize {
		chunk := ids[start:min(start+documentBatchSize, len(ids))]
		keys := make([]string, len(chunk))
		for i, alert := range chunk {
			key, err := snapshotKey(tenant, id, alert)
			if err != nil {
				return nil, err
			}
			keys[i] = key
		}
		rows, err := batch.GetMany(ctx, "merge_members", keys)
		if err != nil {
			return nil, err
		}
		for i, alert := range chunk {
			raw, found := rows[keys[i]]
			if !found {
				continue
			}
			bytes += len(raw)
			if bytes > 32<<20 {
				return nil, policy.ErrUnavailable
			}
			value, err := decodeSnapshot(raw, tenant, id, alert)
			if err != nil {
				return nil, err
			}
			result[alert] = value
		}
	}
	return result, nil
}

// captureMany 保存本次裁决已取得的首次快照；冲突保持旧值，分块失败保留已写入前缀。
func (j *Journal) captureMany(ctx context.Context, current StoredDecision, alerts []domain.Alert) error {
	batch, ok := j.docs.(batchDocuments)
	if !ok {
		for _, alert := range alerts {
			if _, err := j.Capture(ctx, current.Decision.TenantID, current.Decision.ID, alert); err != nil {
				return err
			}
		}
		return nil
	}
	d := current.Decision
	if d.Progress.Phase != "capturing" {
		return policy.ErrConflict
	}
	for start := 0; start < len(alerts); start += documentBatchSize {
		documents := make(map[string]json.RawMessage)
		for _, alert := range alerts[start:min(start+documentBatchSize, len(alerts))] {
			_, found := slices.BinarySearch(d.MemberIDs, alert.AlertID)
			if alert.BKTenantID != d.TenantID || !found {
				return policy.ErrAccess
			}
			a, err := alert.Normalize()
			if err != nil {
				return err
			}
			key, err := snapshotKey(d.TenantID, d.ID, a.AlertID)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(Snapshot{TenantID: d.TenantID, DecisionID: d.ID, Alert: a})
			if err != nil {
				return err
			}
			if len(raw) > 8<<20 {
				return policy.ErrInvalid
			}
			documents[key] = raw
		}
		if err := batch.CreateMany(ctx, "merge_members", documents); err != nil {
			return err
		}
	}
	return nil
}

// advanceCaptured 仅由已确认完整块的执行器调用；游标仍通过原 decision CAS 持久化。
func (j *Journal) advanceCaptured(ctx context.Context, current StoredDecision, through int, at time.Time) (StoredDecision, error) {
	d := current.Decision
	if d.Progress.Phase != "capturing" || through <= d.Progress.CaptureOffset || through > len(d.MemberIDs) {
		return StoredDecision{}, policy.ErrConflict
	}
	next := d.Clone()
	next.Progress.CaptureOffset = through
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

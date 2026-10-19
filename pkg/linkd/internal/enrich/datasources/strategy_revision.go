// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"linkd/internal/domain"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

const maxStrategyRevisionBytes = 4 << 20
const strategyReadTimeout = 5 * time.Second

// strategyRevisionRow 承载指定执行修订；当前 SplitRecord 的状态不参与历史有效性判断。
type strategyRevisionRow struct {
	TenantID      string `gorm:"column:bk_tenant_id"`
	BusinessID    int64  `gorm:"column:strategy_bk_biz_id"`
	StrategyID    int64  `gorm:"column:strategy_id"`
	Version       int64  `gorm:"column:strategy_revision"`
	SchemaVersion int64  `gorm:"column:snapshot_schema_version"`
	Digest        string `gorm:"column:snapshot_digest"`
	Bytes         int64  `gorm:"column:snapshot_bytes"`
	Snapshot      []byte `gorm:"column:snapshot_json"`
}

// 一次有界 SELECT 原子读取历史身份和 canonical bytes。只按事件租户、策略和版本定位，
// 不用事件业务过滤全局策略的管理业务；跨业务存在多行时明确拒绝，不能任选一行。
// Kingeye 是 canonicalizer，摘要校验使用原始字节，不能解码再编码后计算。
func readStrategyRevision(ctx context.Context, db *gorm.DB, query models.StrategyRevisionQuery) (strategyRevisionRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return strategyRevisionRow{}, false, err
	}
	var rows []strategyRevisionRow
	err := db.WithContext(ctx).Table("alarm_strategy_execution_revision").
		Select("bk_tenant_id,strategy_bk_biz_id,strategy_id,strategy_revision,snapshot_schema_version,snapshot_digest,snapshot_bytes,CASE WHEN OCTET_LENGTH(snapshot_json) <= ? THEN snapshot_json ELSE NULL END AS snapshot_json", maxStrategyRevisionBytes).
		Where("bk_tenant_id = ? AND strategy_id = ? AND strategy_revision = ?", query.TenantID, query.ID, query.Version).Limit(2).Find(&rows).Error
	if err != nil {
		return strategyRevisionRow{}, false, fmt.Errorf("read strategy revision: %w", err)
	}
	if len(rows) == 0 {
		return strategyRevisionRow{}, false, nil
	}
	if len(rows) != 1 {
		return strategyRevisionRow{}, false, descriptionFailure("strategy_revision_ambiguous")
	}
	row := rows[0]
	if row.TenantID != query.TenantID || row.StrategyID != query.ID || row.Version != query.Version || row.BusinessID == 0 {
		return strategyRevisionRow{}, false, descriptionFailure("strategy_revision_identity_mismatch")
	}

	digest := sha256.Sum256(row.Snapshot)
	if len(row.Snapshot) == 0 || len(row.Snapshot) > maxStrategyRevisionBytes || int64(len(row.Snapshot)) != row.Bytes || hex.EncodeToString(digest[:]) != row.Digest || !utf8.Valid(row.Snapshot) {
		return strategyRevisionRow{}, false, descriptionFailure("strategy_revision_corrupt")
	}
	if row.SchemaVersion != 1 {
		return strategyRevisionRow{}, false, descriptionFailure("strategy_revision_schema_unsupported")
	}
	// 与 Kingeye Reader 一致：确认支持 schema 后才按当前 JSON 规则检查快照。
	if !json.Valid(row.Snapshot) {
		return strategyRevisionRow{}, false, descriptionFailure("strategy_revision_corrupt")
	}
	return row, true, nil
}

type resolvedStrategy struct {
	Kind     models.CWStrategyKind `json:"kind"`
	Metadata struct {
		UID         string            `json:"uid"`
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations domain.JSONObject `json:"annotations"`
		Labels      struct {
			TenantID         string          `json:"bk_tenant_id"`
			BusinessID       int64           `json:"bk_biz_id"`
			TemplateID       int64           `json:"monitor_template_id"`
			ConfigID         string          `json:"config_id"`
			IsDefault        *bool           `json:"is_default"`
			DefaultConfigUID *string         `json:"default_strategy_config_uid"`
			ObjectModelCode  *string         `json:"object_model_code"`
			ObjectInstanceID json.RawMessage `json:"bk_object_inst_id"`
		} `json:"labels"`
	} `json:"metadata"`
	Status models.CWStrategyStatus `json:"status"`
	Spec   json.RawMessage         `json:"spec"`
}

// 快照是 Kingeye 发布到 Redis 的根部执行详情；schema=1 不代表仍兼容旧表的 strategy wrapper。
type strategyRevisionSnapshot struct {
	ID            int64             `json:"id"`
	BusinessID    int64             `json:"bk_biz_id"`
	TenantID      string            `json:"bk_tenant_id"`
	Version       int64             `json:"strategy_revision"`
	RuntimeConfig resolvedStrategy  `json:"runtime_config"`
	Items         []json.RawMessage `json:"items"`
}

// 执行查询/算法与声明式上下文来自同一不可变快照，不再拼接当前 Set/Config 或 SplitRecord。
func decodeStrategyRevision(row strategyRevisionRow) (strategyRevisionSnapshot, error) {
	var snapshot strategyRevisionSnapshot
	if json.Unmarshal(row.Snapshot, &snapshot) != nil {
		return snapshot, descriptionFailure("strategy_revision_corrupt")
	}
	strategy := snapshot
	if strategy.ID != row.StrategyID || strategy.BusinessID != row.BusinessID || strategy.TenantID != row.TenantID || strategy.Version != row.Version {
		return snapshot, descriptionFailure("strategy_revision_identity_mismatch")
	}
	labels := strategy.RuntimeConfig.Metadata.Labels
	if labels.TenantID != row.TenantID || labels.TemplateID <= 0 || labels.BusinessID == 0 || !validStrategyUUID(labels.ConfigID) || labels.IsDefault == nil || strategy.RuntimeConfig.Status.BKStrategyID != row.StrategyID {
		return snapshot, descriptionFailure("strategy_revision_binding_invalid")
	}
	if len(strategy.Items) == 0 || len(strategy.Items) > 32 {
		return snapshot, descriptionFailure("strategy_revision_items_invalid")
	}
	return snapshot, nil
}

func validStrategyUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil
}

func descriptionFailure(code string) error { return &description.Error{Code: code} }

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"linkd/internal/domain"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

const maxStrategyPublicationBytes = 4 << 20

const strategyReadTimeout = 5 * time.Second

const maxStrategyResolvedConfigs = 32

type strategyPublicationRow struct {
	ID            int64
	ParentID      *int64
	TenantID      string `gorm:"column:bk_tenant_id"`
	SetUID        string `gorm:"column:strategy_set_uid"`
	TemplateID    int64  `gorm:"column:monitor_template_id"`
	ConfigUID     string `gorm:"column:config_uid"`
	Version       int64  `gorm:"column:source_resource_version"`
	State         string
	PublishStatus string
	Enabled       bool
	BusinessIDs   datatypes.JSON `gorm:"column:bk_biz_ids"`
	Payload       datatypes.JSON
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
	Spec json.RawMessage `json:"spec"`
}

type strategyPublication struct {
	SchemaVersion  int             `json:"schema_version"`
	CompileError   json.RawMessage `json:"compile_error"`
	ExecutionState string          `json:"execution_state"`
	Set            struct {
		UID                string `json:"uid"`
		TemplateID         int64  `json:"monitor_template_id"`
		ConfigType         string `json:"config_type"`
		TemplateBusinessID int64  `json:"template_bk_biz_id"`
	} `json:"strategy_set"`
	Config   json.RawMessage    `json:"strategy_config"`
	Resolved []resolvedStrategy `json:"resolved_strategies"`
	// Runtime 只供文案入口读取；普通 Enrich 不解码不使用的运行时字段，
	// 避免运行时 DTO 的字段形态阻断已验证的 resolved 策略投影。
	Runtime []json.RawMessage `json:"runtime_query_configs"`
}

// 单条 SELECT 同时读取版本、状态和完整发布材料，避免跨查询混入不同调和轮次。
// 只接受显式租户和拆分主键；载荷在 SQL 端限长，不查询或回退旧配置表。
func readStrategyPublication(ctx context.Context, db *gorm.DB, query models.StrategyQuery) (strategyPublicationRow, bool, error) {
	var rows []strategyPublicationRow
	err := db.WithContext(ctx).Table("alarm_strategy_set_split_record").
		Select("id,parent_id,bk_tenant_id,strategy_set_uid,monitor_template_id,config_uid,source_resource_version,state,publish_status,enabled,CASE WHEN OCTET_LENGTH(bk_biz_ids) <= 1024 THEN bk_biz_ids ELSE NULL END AS bk_biz_ids,CASE WHEN OCTET_LENGTH(payload) <= ? THEN payload ELSE NULL END AS payload", maxStrategyPublicationBytes).
		Where("bk_tenant_id = ? AND id = ? AND source_resource_version = ?", query.TenantID, query.ID, query.Version).Limit(2).Find(&rows).Error
	if err != nil {
		return strategyPublicationRow{}, false, fmt.Errorf("read strategy publication: %w", err)
	}
	if len(rows) == 0 {
		return strategyPublicationRow{}, false, nil
	}
	if len(rows) != 1 {
		return strategyPublicationRow{}, false, descriptionFailure("publication_missing_or_ambiguous")
	}
	row := rows[0]
	if row.ID != query.ID || row.TenantID != query.TenantID || row.Version != query.Version {
		return strategyPublicationRow{}, false, descriptionFailure("publication_version_mismatch")
	}
	// 删除或禁用不抹去已触发事件的发布身份；普通丰富只校验发布完成与版本绑定。
	if row.PublishStatus != "published" {
		return strategyPublicationRow{}, false, descriptionFailure("publication_not_active")
	}
	if len(row.Payload) == 0 || len(row.Payload) > maxStrategyPublicationBytes {
		return strategyPublicationRow{}, false, descriptionFailure("publication_payload_invalid")
	}
	return row, true, nil
}

// 当前发布态只保留一个版本；版本不符不能用最新内容替代。覆盖载荷没有 Set/Config
// 副本，普通丰富直接使用其 resolved_strategies；文案入口另行拒绝缺少独立修订的覆盖。
func decodeStrategyPublication(row strategyPublicationRow) (strategyPublication, error) {
	var payload strategyPublication
	if json.Unmarshal(row.Payload, &payload) != nil || payload.SchemaVersion != 1 || len(payload.CompileError) > 0 || payload.ExecutionState != "" {
		return payload, descriptionFailure("publication_payload_invalid")
	}
	if row.TemplateID <= 0 || !sameStrategyUUID(row.SetUID, row.SetUID) || !sameStrategyUUID(row.ConfigUID, row.ConfigUID) {
		return payload, descriptionFailure("publication_binding_invalid")
	}
	if row.ParentID == nil && (!sameStrategyUUID(row.SetUID, payload.Set.UID) || row.TemplateID != payload.Set.TemplateID || (payload.Set.ConfigType != "data" && payload.Set.ConfigType != "target")) {
		return payload, descriptionFailure("publication_binding_invalid")
	}
	if row.ParentID == nil {
		var config models.StrategySetConfig
		if json.Unmarshal(payload.Config, &config) != nil || !sameStrategyUUID(config.ID, row.ConfigUID) {
			return payload, descriptionFailure("publication_binding_invalid")
		}
	} else if *row.ParentID <= 0 || *row.ParentID == row.ID {
		return payload, descriptionFailure("publication_binding_invalid")
	}
	if len(payload.Resolved) == 0 || len(payload.Resolved) > maxStrategyResolvedConfigs {
		return payload, descriptionFailure("publication_runtime_invalid")
	}
	var businesses []int64
	if json.Unmarshal(row.BusinessIDs, &businesses) != nil || len(businesses) == 0 || len(businesses) > maxStrategyResolvedConfigs {
		return payload, descriptionFailure("publication_business_invalid")
	}
	allowed := make(map[int64]bool, len(businesses))
	for _, business := range businesses {
		if business == 0 || allowed[business] {
			return payload, descriptionFailure("publication_business_invalid")
		}
		allowed[business] = true
	}
	if payload.Set.ConfigType == "target" && len(payload.Resolved) != 1 {
		return payload, descriptionFailure("publication_target_items_unverified")
	}
	seen := make(map[int64]bool, len(payload.Resolved))
	for _, entry := range payload.Resolved {
		labels := entry.Metadata.Labels
		if labels.TenantID != row.TenantID || labels.TemplateID != row.TemplateID || !sameStrategyUUID(labels.ConfigID, row.ConfigUID) || labels.IsDefault == nil || *labels.IsDefault != (row.ParentID == nil) || !allowed[labels.BusinessID] || seen[labels.BusinessID] {
			return payload, descriptionFailure("publication_binding_invalid")
		}
		seen[labels.BusinessID] = true
		// DATA 多业务投影在 Kingeye 中折叠为首项；必须先证明完整 spec 相同。
		if len(payload.Resolved) > 1 && !strategyJSONEqual(entry.Spec, payload.Resolved[0].Spec) {
			return payload, descriptionFailure("publication_business_specs_differ")
		}
	}
	if payload.Set.ConfigType != "target" && len(seen) != len(allowed) {
		return payload, descriptionFailure("publication_business_mismatch")
	}
	return payload, nil
}

func sameStrategyUUID(a, b string) bool {
	left, err := uuid.Parse(a)
	if err != nil || left == uuid.Nil {
		return false
	}
	right, err := uuid.Parse(b)
	return err == nil && right != uuid.Nil && left == right
}

func strategyJSONEqual(a, b []byte) bool {
	decode := func(raw []byte) (map[string]any, bool) {
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		err := decoder.Decode(&value)
		return value, err == nil && value != nil
	}
	left, ok := decode(a)
	if !ok {
		return false
	}
	right, ok := decode(b)
	return ok && reflect.DeepEqual(left, right)
}

func descriptionFailure(code string) error { return &description.Error{Code: code} }

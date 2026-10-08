// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

const maxDescriptionPublicationBytes = 4 << 20
const descriptionReadTimeout = 5 * time.Second
const maxDescriptionResolvedConfigs = 32

// DescriptionConfigurationClient 读取 Set 拆分发布身份，再校验当前 Set/Config 的渲染依赖。
// 只读取 current 表；无法证明旧版本时明确失败，不读取历史表或复制来源文案。
type DescriptionConfigurationClient struct{ db *gorm.DB }

var _ description.ConfigurationReader = (*DescriptionConfigurationClient)(nil)

// NewDescriptionConfigurationClient 注入由进程管理的数据库连接；不迁移 schema。
func NewDescriptionConfigurationClient(db *gorm.DB) (*DescriptionConfigurationClient, error) {
	if db == nil {
		return nil, fmt.Errorf("description configuration requires database")
	}
	return &DescriptionConfigurationClient{db: db}, nil
}

type descriptionPublicationRow struct {
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

type descriptionPublication struct {
	SchemaVersion int             `json:"schema_version"`
	CompileError  json.RawMessage `json:"compile_error"`
	Set           struct {
		UID                string `json:"uid"`
		TemplateID         int64  `json:"monitor_template_id"`
		ConfigType         string `json:"config_type"`
		TemplateBusinessID int64  `json:"template_bk_biz_id"`
	} `json:"strategy_set"`
	Config   json.RawMessage `json:"strategy_config"`
	Resolved []struct {
		Metadata struct {
			Labels struct {
				TenantID   string `json:"bk_tenant_id"`
				BusinessID int64  `json:"bk_biz_id"`
				TemplateID int64  `json:"monitor_template_id"`
				ConfigID   string `json:"config_id"`
				IsDefault  *bool  `json:"is_default"`
			} `json:"labels"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	} `json:"resolved_strategies"`
	Runtime []struct {
		Error   json.RawMessage `json:"error"`
		Queries []struct {
			Unit     string `json:"unit"`
			MetricID string `json:"metric_id"`
		} `json:"query_configs"`
		Algorithms []description.RuntimeAlgorithm `json:"algorithms"`
	} `json:"runtime_query_configs"`
}

// ReadConfiguration 使用只读 repeatable-read 事务，避免发布记录、Set 与 Config
// 分别读到不同轮次。所有 SQL 都显式按租户隔离并限制返回数量和 JSON 大小。
// targets 和查询的 log_theme_list 用于对象筛选、日志查询/丰富；描述输入中的
// 查询只读取 PromQL 名称，以及发布材料冻结的单位和算法，故不比较这两个上下文字段。
// 其他字段（含未知扩展）全部保留校验。
func (c *DescriptionConfigurationClient) ReadConfiguration(ctx context.Context, query description.ConfigurationQuery) (description.Configuration, error) {
	if ctx == nil || !query.Valid() {
		return description.Configuration{}, descriptionFailure("configuration_identity_invalid")
	}
	if err := ctx.Err(); err != nil {
		return description.Configuration{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, descriptionReadTimeout)
	defer cancel()
	var result description.Configuration
	err := c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []descriptionPublicationRow
		err := tx.Table("alarm_strategy_set_split_record").Select("id,parent_id,bk_tenant_id,strategy_set_uid,monitor_template_id,config_uid,source_resource_version,state,publish_status,enabled,bk_biz_ids,CASE WHEN OCTET_LENGTH(payload) <= ? THEN payload ELSE NULL END AS payload", maxDescriptionPublicationBytes).
			Where("bk_tenant_id = ? AND id = ?", query.TenantID, query.StrategyID).Limit(2).Find(&rows).Error
		if err != nil {
			return fmt.Errorf("read description publication: %w", err)
		}
		if len(rows) != 1 {
			return descriptionFailure("publication_missing_or_ambiguous")
		}
		row := rows[0]
		if row.ID != query.StrategyID || row.TenantID != query.TenantID || row.Version != query.StrategyVersion {
			return descriptionFailure("publication_version_mismatch")
		}
		if row.ParentID != nil {
			// Kingeye 覆盖修改会复用 parent 的 source_resource_version；标准输出
			// 没有覆盖配置摘要或修改代次，无法用此整数证明旧事件的检测参数。
			// 不将当前覆盖或默认配置冒充触发时版本，等待稳定版本事实来源。
			return descriptionFailure("publication_override_revision_missing")
		}
		if row.State != "active" || row.PublishStatus != "published" || !row.Enabled {
			return descriptionFailure("publication_not_active")
		}
		if len(row.Payload) == 0 || len(row.Payload) > maxDescriptionPublicationBytes {
			return descriptionFailure("publication_payload_invalid")
		}
		var payload descriptionPublication
		if json.Unmarshal(row.Payload, &payload) != nil || payload.SchemaVersion != 1 || len(payload.CompileError) > 0 {
			return descriptionFailure("publication_payload_invalid")
		}
		if !sameDescriptionUUID(row.SetUID, payload.Set.UID) || row.TemplateID <= 0 || row.TemplateID != payload.Set.TemplateID {
			return descriptionFailure("publication_binding_invalid")
		}
		if payload.Set.ConfigType != "data" && payload.Set.ConfigType != "target" {
			return descriptionFailure("publication_config_type_invalid")
		}
		if len(payload.Resolved) == 0 || len(payload.Resolved) > maxDescriptionResolvedConfigs || len(payload.Runtime) != len(payload.Resolved) {
			return descriptionFailure("publication_runtime_invalid")
		}
		var businesses []int64
		if json.Unmarshal(row.BusinessIDs, &businesses) != nil || len(businesses) == 0 || len(businesses) > maxDescriptionResolvedConfigs {
			return descriptionFailure("publication_business_invalid")
		}
		allowed := make(map[int64]bool, len(businesses))
		for _, business := range businesses {
			if business == 0 || allowed[business] {
				return descriptionFailure("publication_business_invalid")
			}
			allowed[business] = true
		}
		// 发布配置的业务决定读取哪份 resolved spec，不由事件业务反查配置。
		// standard labels.bk_biz_id 是事件业务；真实跨业务观测可以与模板业务不同。
		// 配置身份始终由同租户的 split ID/version、Set UID、template 和 Config UUID 绑定。
		home := businesses[0]
		spansBusinesses := payload.Set.ConfigType == "data" && len(businesses) > 1
		if spansBusinesses && payload.Set.TemplateBusinessID != 0 {
			home = payload.Set.TemplateBusinessID
		}
		target := payload.Set.ConfigType == "target"
		if target && len(payload.Resolved) != 1 {
			return descriptionFailure("publication_target_items_unverified")
		}
		selected := -1
		seen := make(map[int64]bool, len(payload.Resolved))
		for index, entry := range payload.Resolved {
			labels := entry.Metadata.Labels
			if labels.TenantID != query.TenantID || labels.TemplateID != row.TemplateID || !sameDescriptionUUID(labels.ConfigID, row.ConfigUID) || labels.IsDefault == nil || !*labels.IsDefault || !allowed[labels.BusinessID] || seen[labels.BusinessID] {
				return descriptionFailure("publication_binding_invalid")
			}
			seen[labels.BusinessID] = true
			if spansBusinesses {
				if !descriptionJSONEqual(entry.Spec, payload.Resolved[0].Spec, false, false) {
					return descriptionFailure("publication_business_specs_differ")
				}
				selected = 0
			} else if labels.BusinessID == home {
				if selected >= 0 {
					return descriptionFailure("publication_business_ambiguous")
				}
				selected = index
			}
		}
		if selected < 0 || (!target && len(seen) != len(allowed)) {
			return descriptionFailure("publication_business_mismatch")
		}
		set, config, err := c.readBoundCurrent(tx, query, row, payload, selected)
		if err != nil {
			return err
		}
		runtime := payload.Runtime[selected]
		if len(runtime.Error) > 0 || len(runtime.Queries) == 0 || len(runtime.Queries) > 32 || len(runtime.Algorithms) > 32 {
			return descriptionFailure("publication_runtime_invalid")
		}
		queries := make([]models.StrategyQueryConfig, len(runtime.Queries))
		for index, query := range runtime.Queries {
			queries[index].Unit = query.Unit
			queries[index].MetricID = query.MetricID
		}
		result = description.Configuration{Identity: query, Spec: config, SetConfig: set, Queries: queries, Algorithms: runtime.Algorithms}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return description.Configuration{}, err
	}
	return result, nil
}

func (c *DescriptionConfigurationClient) readBoundCurrent(tx *gorm.DB, query description.ConfigurationQuery, row descriptionPublicationRow, payload descriptionPublication, selected int) (models.StrategySetConfig, models.CWStrategySpec, error) {
	var sets []strategySetRow
	err := tx.Table("core_v1alpha1_strategyset").Select("uid,bk_tenant_id,monitor_template_id,kind,api_version,CASE WHEN OCTET_LENGTH(spec) <= ? THEN spec ELSE NULL END AS spec", maxStrategySetSpecBytes).
		Where("bk_tenant_id = ? AND uid = ? AND monitor_template_id = ? AND active = ?", query.TenantID, compactDescriptionUUID(row.SetUID), row.TemplateID, true).Limit(2).Find(&sets).Error
	if err != nil {
		return models.StrategySetConfig{}, models.CWStrategySpec{}, fmt.Errorf("read bound description set: %w", err)
	}
	if len(sets) != 1 || sets[0].BKTenantID != query.TenantID || !sameDescriptionUUID(sets[0].UID, row.SetUID) || sets[0].MonitorTemplateID != row.TemplateID || sets[0].Kind != "StrategySet" || sets[0].APIVersion != "v1alpha1" || len(sets[0].Spec) == 0 {
		return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_set_invalid")
	}
	var setSpec struct {
		TemplateID int64             `json:"monitor_template_id"`
		Configs    []json.RawMessage `json:"strategy_configs"`
	}
	if json.Unmarshal(sets[0].Spec, &setSpec) != nil || setSpec.TemplateID != row.TemplateID || len(setSpec.Configs) > maxStrategySetConfigs {
		return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_set_invalid")
	}
	var rawConfig json.RawMessage
	for _, candidate := range setSpec.Configs {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(candidate, &identity) != nil {
			return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_set_invalid")
		}
		if sameDescriptionUUID(identity.ID, row.ConfigUID) {
			if rawConfig != nil {
				return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_set_ambiguous")
			}
			rawConfig = candidate
		}
	}
	if rawConfig == nil || !descriptionJSONEqual(rawConfig, payload.Config, false, true) {
		return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_set_changed")
	}
	var configRows []struct {
		TenantID   string `gorm:"column:bk_tenant_id"`
		TemplateID int64  `gorm:"column:monitor_template_id"`
		ConfigID   string `gorm:"column:config_id"`
		BusinessID int64  `gorm:"column:bk_biz_id"`
		Kind       string
		APIVersion string
		Spec       datatypes.JSON
	}
	// 同一 default Config UUID 可以有多份业务投影。只有各份渲染依赖均等于
	// 冻结配置时才能使用；不能选最新一份或按事件业务猜测配置。重复业务和
	// 超限候选仍拒绝，保证不会靠不完整的候选集合证明一致性。
	err = tx.Table("core_v1alpha1_strategy").Select("bk_tenant_id,monitor_template_id,config_id,bk_biz_id,kind,api_version,CASE WHEN OCTET_LENGTH(spec) <= ? THEN spec ELSE NULL END AS spec", maxStrategySetSpecBytes).
		Where("bk_tenant_id = ? AND monitor_template_id = ? AND config_id = ? AND active = ? AND is_default = ?", query.TenantID, row.TemplateID, compactDescriptionUUID(row.ConfigUID), true, true).Limit(maxDescriptionResolvedConfigs + 1).Find(&configRows).Error
	if err != nil {
		return models.StrategySetConfig{}, models.CWStrategySpec{}, fmt.Errorf("read bound description config: %w", err)
	}
	if len(configRows) == 0 || len(configRows) > maxDescriptionResolvedConfigs {
		return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_config_missing_or_ambiguous")
	}
	seenBusinesses := make(map[int64]bool, len(configRows))
	for _, current := range configRows {
		// Cloud 与普通策略共用既有 StrategyConfig 表；描述读取相同的冻结
		// 检测字段，云资源字段仍纳入完整 JSON 依赖校验。
		validKind := current.Kind == "Strategy" || current.Kind == string(models.CWStrategyKindCloud)
		if current.TenantID != query.TenantID || current.TemplateID != row.TemplateID || !sameDescriptionUUID(current.ConfigID, row.ConfigUID) || current.BusinessID == 0 || !validKind || current.APIVersion != "v1alpha1" || len(current.Spec) == 0 {
			return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_config_invalid")
		}
		if seenBusinesses[current.BusinessID] {
			return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_config_missing_or_ambiguous")
		}
		seenBusinesses[current.BusinessID] = true
		if !descriptionJSONEqual(current.Spec, payload.Resolved[selected].Spec, true, false) {
			return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_config_changed")
		}
	}
	var setConfig models.StrategySetConfig
	var spec models.CWStrategySpec
	// 描述只使用冻结 spec；current 候选用于证明其依赖没有变化。
	if json.Unmarshal(rawConfig, &setConfig) != nil || json.Unmarshal(payload.Resolved[selected].Spec, &spec) != nil || !setConfig.Enable || spec.Enable == nil || !*spec.Enable {
		return models.StrategySetConfig{}, models.CWStrategySpec{}, descriptionFailure("configuration_disabled_or_invalid")
	}
	return setConfig, spec, nil
}

func descriptionFailure(code string) error { return &description.Error{Code: code} }

func sameDescriptionUUID(a, b string) bool {
	left, err := uuid.Parse(a)
	if err != nil || left == uuid.Nil {
		return false
	}
	right, err := uuid.Parse(b)
	return err == nil && right != uuid.Nil && left == right
}

func compactDescriptionUUID(value string) string {
	id, err := uuid.Parse(value)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", id[:])
}

// 严格比较 JSON；renderSpec 仅排除已证明不进入描述事实的配置上下文。
// UseNumber 避免大整数先转 float64 后把不同版本或阈值误判相同。
func descriptionJSONEqual(a, b []byte, renderSpec bool, normalizeID bool) bool {
	decode := func(raw []byte) (map[string]any, bool) {
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || value == nil {
			return nil, false
		}
		if renderSpec {
			delete(value, "targets")
			if item, ok := value["strategy_item"].(map[string]any); ok {
				if queries, ok := item["query_configs"].([]any); ok {
					for _, query := range queries {
						if fields, ok := query.(map[string]any); ok {
							delete(fields, "log_theme_list")
						}
					}
				}
			}
		}
		if normalizeID {
			id, ok := value["id"].(string)
			if !ok || !sameDescriptionUUID(id, id) {
				return nil, false
			}
			value["id"] = compactDescriptionUUID(id)
		}
		return value, true
	}
	left, ok := decode(a)
	if !ok {
		return false
	}
	right, ok := decode(b)
	return ok && reflect.DeepEqual(left, right)
}

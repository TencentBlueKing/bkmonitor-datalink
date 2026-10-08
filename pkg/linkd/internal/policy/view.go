// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/models"
	enrichview "linkd/internal/enrich/view"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
)

// LevelMapper 冻结本次评估的标准等级到 KAC 等级映射；不能在逐条件读取时加载不同版本。
type LevelMapper func(string) (string, error)

// DefaultKACLevel 保留默认等级映射；自定义或 native 名称由装配传入显式 Mapper。
func DefaultKACLevel(name string) (string, error) {
	switch name {
	case "critical":
		return "fatal", nil
	case "warning":
		return "warning", nil
	case "info":
		return "remind", nil
	default:
		return "", ErrUnavailable
	}
}

// RelationLookup 提供当前租户中从明确主实例到指定模型的双向关联目标。
// origin 由主告警有效视图提供，不能由外部配置伪造 __origin_*。
type RelationLookup interface {
	Lookup(context.Context, string, onemodel.InstanceRef, string, string) ([]onemodel.InstanceRef, error)
}

// RelationContext 只在依赖策略的主/被屏蔽候选对比时提供起点。
type RelationContext struct {
	Origin onemodel.InstanceRef
	Lookup RelationLookup
}

type uncertainPath struct {
	target jsonpath.Target
	step   int
}

type certainPath struct {
	target jsonpath.Target
	step   int
}

// FactView 是某个 Event evaluation 或 Alert 的只读有效字段视图，不读取原始 payload。
// 失败处理器影响的字段保持不可求值，不能被缺失判断或否定条件绕过。
type FactView struct {
	TenantID   string
	document   map[string]any
	facts      map[string]any
	levelError bool
	mappings   map[string]jsonpath.Target
	uncertain  []uncertainPath
	certain    []certainPath
	relation   RelationContext
}

// EventView 读取指定等级已经冻结的丰富结果；不会重新运行 Enrich。
func EventView(event domain.Event, severity string, mappings map[string]FieldMapping, level LevelMapper, relation RelationContext) (*FactView, error) {
	result, found := event.ForSeverity(severity)
	if !found {
		return nil, ErrUnavailable
	}
	var evaluation domain.EventEvaluation
	for _, item := range event.Evaluations {
		if item.Severity == severity {
			evaluation = item
		}
	}
	if evaluation.Severity == "" {
		return nil, ErrUnavailable
	}
	effective, err := enrichview.EnrichedEvent(event, severity)
	if err != nil {
		return nil, err
	}
	document, err := domain.EventDocument(effective, evaluation)
	if err != nil {
		return nil, err
	}
	return newFactView(event.BKTenantID, document, severity, string(evaluation.Action), event.OccurredAt, result.Status, result.Data, mappings, level, relation)
}

// AlertView 只使用 opening Event 的固定丰富快照，当前 severity/action 仍来自 Alert 的真实状态。
func AlertView(alert domain.Alert, mappings map[string]FieldMapping, level LevelMapper, relation RelationContext) (*FactView, error) {
	effective, err := enrichview.EnrichedAlert(alert)
	if err != nil {
		return nil, err
	}
	document, err := domain.AlertDocument(effective)
	if err != nil {
		return nil, err
	}
	action := "triggered"
	switch alert.Status {
	case domain.AlertStatusRecovered:
		action = "resolved"
	case domain.AlertStatusClosed:
		action = "closed"
	case domain.AlertStatusActive:
	default:
		return nil, ErrUnavailable
	}
	return newFactView(alert.BKTenantID, document, alert.Severity, action, alert.BeginAt, alert.EnrichStatus, alert.Enrich, mappings, level, relation)
}

func newFactView(tenant string, document map[string]any, severity, action string, occurred time.Time, status domain.EnrichStatus, payload domain.JSONObject, mappings map[string]FieldMapping, level LevelMapper, relation RelationContext) (*FactView, error) {
	if err := domain.ValidateIdentityPart("bk_tenant_id", tenant, 64); err != nil {
		return nil, err
	}
	if level == nil {
		level = DefaultKACLevel
	}
	kacLevel, levelErr := level(severity)
	kacAction := map[string]string{"triggered": "firing", "resolved": "resolved", "closed": "close"}[action]
	if kacAction == "" || occurred.IsZero() {
		return nil, ErrUnavailable
	}
	view := &FactView{TenantID: tenant, document: document, facts: map[string]any{"bk_tenant_id": tenant, "level": kacLevel, "action": kacAction, "alarm_time": occurred.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04:05")}, levelError: levelErr != nil, mappings: map[string]jsonpath.Target{}, relation: relation}
	for name, mapping := range mappings {
		target, err := jsonpath.ParseTarget(mapping.Path)
		if err != nil {
			return nil, err
		}
		parts := target.Parts()
		if len(parts) < 2 || (parts[0] != "labels" && parts[0] != "extra_data") || KACFields()[name] != "" {
			return nil, fmt.Errorf("invalid policy field mapping")
		}
		view.mappings[name] = target
	}
	if err := view.readAvailability(status, payload); err != nil {
		return nil, err
	}
	return view, nil
}

func pathFor(parts ...string) jsonpath.Target {
	path := "$"
	for _, part := range parts {
		encoded, _ := json.Marshal(part)
		path += "[" + string(encoded) + "]"
	}
	target, _ := jsonpath.ParseTarget(path)
	return target
}

func (v *FactView) readAvailability(status domain.EnrichStatus, payload domain.JSONObject) error {
	var entries []map[string]struct {
		Status  domain.EnrichStatus  `json:"status"`
		Patches []domain.EnrichPatch `json:"patches"`
		Value   domain.JSONObject    `json:"value"`
	}
	if raw := payload["processors"]; raw != nil {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
	}
	block := func(paths []jsonpath.Target, step int) {
		for _, target := range paths {
			v.uncertain = append(v.uncertain, uncertainPath{target, step})
		}
	}
	broad := []jsonpath.Target{pathFor("title"), pathFor("content"), pathFor("subject_name"), pathFor("labels"), pathFor("extra_data")}
	if len(entries) == 0 && (status == domain.EnrichStatusFailed || status == domain.EnrichStatusPartial || status == domain.EnrichStatusPending) {
		block(broad, 0)
	}
	for i, entry := range entries {
		if len(entry) != 1 {
			return ErrUnavailable
		}
		for name, result := range entry {
			if result.Status == domain.EnrichStatusFailed || result.Status == domain.EnrichStatusPartial {
				paths := processorPaths(name)
				if paths == nil {
					paths = broad
				}
				block(paths, i+1)
			}
			if result.Status != domain.EnrichStatusSucceeded {
				continue
			}
			patches := result.Patches
			var err error
			if patches == nil && result.Value != nil {
				patches, err = kingeye.ProjectValue(name, result.Value)
				if err != nil {
					return err
				}
			}
			for _, patch := range patches {
				target, err := jsonpath.ParseTarget(patch.Path)
				if err != nil {
					return err
				}
				v.certain = append(v.certain, certainPath{target, i + 1})
			}
		}
	}
	return nil
}

func processorPaths(name string) []jsonpath.Target {
	var value any
	switch name {
	case "resource":
		value = models.ResourceValues{}
	case "strategy":
		value = models.StrategyValues{}
	case "source":
		value = models.SourceValues{}
	case "display":
		value = models.DisplayValues{}
	case "metric":
		value = models.MetricValues{}
	case "log":
		value = models.LogValues{}
	case "apm":
		value = models.APMValues{}
	case "k8s":
		value = models.K8sValues{}
	default:
		return nil
	}
	typ := reflect.TypeOf(value)
	paths := []jsonpath.Target{}
	for i := 0; i < typ.NumField(); i++ {
		field := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if field == "" {
			continue
		}
		if name == "display" {
			switch field {
			case "title", "content":
				paths = append(paths, pathFor(field))
				continue
			case "object":
				paths = append(paths, pathFor("subject_name"))
				continue
			}
		}
		paths = append(paths, pathFor("labels", field), pathFor("extra_data", field))
	}
	return paths
}

func (v *FactView) readPath(target jsonpath.Target) (Value, error) {
	blocked := 0
	for _, item := range v.uncertain {
		if item.target.Overlaps(target) {
			blocked = max(blocked, item.step)
		}
	}
	if blocked > 0 || len(v.uncertain) > 0 {
		uncertain := false
		for _, item := range v.uncertain {
			if item.target.Overlaps(target) {
				uncertain = true
			}
		}
		if uncertain {
			cleared := false
			for _, item := range v.certain {
				if item.step > blocked && item.target.Overlaps(target) && len(item.target.Parts()) <= len(target.Parts()) {
					cleared = true
				}
			}
			if !cleared {
				return Value{}, ErrUnavailable
			}
		}
	}
	value, present := target.Get(v.document)
	return Value{Data: jsonpath.Clone(value), Present: present}, nil
}

func (v *FactView) flat(name string) (Value, error) {
	label, err := v.readPath(pathFor("labels", name))
	if err != nil {
		return Value{}, err
	}
	if label.Present {
		return label, nil
	}
	return v.readPath(pathFor("extra_data", name))
}

// Field 使用明确的 KAC 字段映射，canonical 身份不从 SubjectType 或 EventSourceID 猜测。
func (v *FactView) Field(ctx context.Context, name string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if name == "level" && v.levelError {
		return Value{}, ErrUnavailable
	}
	if value, ok := v.facts[name]; ok {
		return Value{Data: value, Present: true}, nil
	}
	if target, ok := v.mappings[name]; ok {
		return v.readPath(target)
	}
	if KACFields()[name] == "" {
		return Value{}, fmt.Errorf("unknown policy field")
	}
	switch name {
	case "name":
		return v.readPath(pathFor("title"))
	case "content":
		return v.readPath(pathFor("content"))
	case "object":
		value, err := v.readPath(pathFor("subject_name"))
		if err != nil {
			return Value{}, err
		}
		if value.Present && value.Data != "" {
			return value, nil
		}
		return v.readPath(pathFor("subject_id"))
	case "item":
		first, err := v.flat("display_name")
		if err != nil {
			return Value{}, err
		}
		if first.Present && first.Data != "" {
			return first, nil
		}
		return v.flat("strategy_name")
	case "strategy":
		return v.flat("strategy_name")
	case "strategy_id":
		return v.flat("monitor_template_id")
	case "dimension_info":
		return v.flat("dimension_text")
	case "bk_biz_id":
		value, err := v.flat(name)
		if err != nil || value.Present {
			return value, err
		}
		return v.readPath(pathFor("dimensions", "bk_biz_id"))
	case "entity_uid":
		model, err := v.Field(ctx, "model_id")
		if err != nil {
			return Value{}, err
		}
		id, err := v.Field(ctx, "model_inst_id")
		if err != nil {
			return Value{}, err
		}
		m, mOK := model.Data.(string)
		i, iOK := id.Data.(string)
		if !model.Present || !id.Present {
			return Value{}, nil
		}
		if !mOK || !iOK || m == "" || i == "" {
			return Value{}, ErrUnavailable
		}
		return Value{Data: m + "|" + i, Present: true}, nil
	default:
		return v.flat(name)
	}
}

// CanonicalInstance 返回明确来源的 canonical 实例，不把字段缺失视为默认主机。
func (v *FactView) CanonicalInstance(ctx context.Context) (onemodel.InstanceRef, bool, error) {
	m, err := v.Field(ctx, "model_id")
	if err != nil {
		return onemodel.InstanceRef{}, false, err
	}
	i, err := v.Field(ctx, "model_inst_id")
	if err != nil {
		return onemodel.InstanceRef{}, false, err
	}
	if !m.Present || !i.Present || m.Data == "" || i.Data == "" {
		return onemodel.InstanceRef{}, false, nil
	}
	model, mOK := m.Data.(string)
	id, iOK := i.Data.(string)
	if !mOK || !iOK {
		return onemodel.InstanceRef{}, false, ErrUnavailable
	}
	ref := onemodel.InstanceRef{ModelID: model, InstanceID: id, EntityUID: model + "|" + id}
	descriptor := onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: model, Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: []onemodel.InstanceRef{ref}}}}
	if err := descriptor.Validate(); err != nil {
		return onemodel.InstanceRef{}, false, ErrUnavailable
	}
	return ref, true, nil
}

// Related 检查候选是否位于主告警的双向关系目标集合中；缺少主起点不能降级成普通字段比较。
func (v *FactView) Related(ctx context.Context, condition Condition) (bool, error) {
	if v.relation.Lookup == nil || v.relation.Origin.EntityUID == "" {
		return false, ErrUnavailable
	}
	var model string
	if err := json.Unmarshal(condition.Value, &model); err != nil {
		return false, ErrUnavailable
	}
	candidate, found, err := v.CanonicalInstance(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if candidate.ModelID != model {
		return false, nil
	}
	refs, err := v.relation.Lookup.Lookup(ctx, v.TenantID, v.relation.Origin, condition.Relation, model)
	if err != nil {
		return false, err
	}
	if len(refs) > 10000 {
		return false, ErrUnavailable
	}
	matched := false
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		d := onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: model, Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: []onemodel.InstanceRef{ref}}}}
		if d.Validate() != nil || seen[ref.EntityUID] {
			return false, ErrUnavailable
		}
		seen[ref.EntityUID] = true
		matched = matched || ref == candidate
	}
	// 完整集合校验成功后才提交命中，不能因前面的成员匹配而忽略后面的损坏身份。
	return matched, nil
}

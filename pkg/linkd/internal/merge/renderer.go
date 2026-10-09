// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/jsonpath"
	"linkd/internal/policy"
	"linkd/internal/runtimeconfig"
)

// renderParent 使用已持久化的成员快照、策略和显式来源 Release 构造内部 Event。
// 返回值尚未持久化或入队，调用方须先 Journal.Prepare；重试已 prepared 操作必须复用保存的完整 Event。
func renderParent(ctx context.Context, decision Decision, members []Snapshot, source eventsource.Release, severity runtimeconfig.Snapshot) (domain.Event, error) {
	if err := ctx.Err(); err != nil {
		return domain.Event{}, err
	}
	if err := decision.Validate(); err != nil {
		return domain.Event{}, err
	}
	if decision.Progress.Phase != "capturing" || decision.Outcome != "succeeded" || len(members) != len(decision.MemberIDs) {
		return domain.Event{}, policy.ErrInvalid
	}
	if source.ID != domain.BuiltinMergeEventSourceID || source.Spec.EventSourceID != source.ID || source.Version < 1 || source.Spec.Version != source.Version || source.Spec.Storage.Type != config.StorageTypeInternalMerge {
		return domain.Event{}, policy.ErrInvalid
	}
	if source.Spec.RelatedTenantID != "" && source.Spec.RelatedTenantID != decision.TenantID {
		return domain.Event{}, policy.ErrAccess
	}
	if source.Deleted || !source.Spec.Enabled {
		return domain.Event{}, policy.ErrUnavailable
	}
	severity, err := severity.Normalize()
	if err != nil {
		return domain.Event{}, err
	}
	if err := config.ValidateEventSources([]config.EventSource{source.Spec}, severity.Severity); err != nil {
		return domain.Event{}, err
	}
	compiled, err := policy.Compile(policy.Merge, decision.Policy.Spec)
	if err != nil {
		return domain.Event{}, err
	}
	views := make([]policy.TemplateReader, 0, len(members))
	bytes := 0
	for i, member := range members {
		if err := ctx.Err(); err != nil {
			return domain.Event{}, err
		}
		if member.TenantID != decision.TenantID || member.Alert.BKTenantID != decision.TenantID || member.DecisionID != decision.ID {
			return domain.Event{}, policy.ErrAccess
		}
		if member.Alert.AlertID != decision.MemberIDs[i] || member.Alert.Validate() != nil {
			return domain.Event{}, policy.ErrInvalid
		}
		// 全量校验成员输入预算，不能只校验模板实际读取的字段而容忍无界快照。
		raw, err := json.Marshal(member)
		if err != nil {
			return domain.Event{}, err
		}
		bytes += len(raw)
		if bytes > 32<<20 {
			return domain.Event{}, fmt.Errorf("merge snapshots exceed 32 MiB")
		}
		view, err := policy.AlertView(member.Alert, compiled.Common.FieldMappings, severity.KACLevel, policy.RelationContext{})
		if err != nil {
			return domain.Event{}, err
		}
		views = append(views, view)
	}
	document, err := compiled.Template.Render(ctx, views)
	if err != nil {
		return domain.Event{}, err
	}
	level, ok := document["severity"].(string)
	if !ok {
		return domain.Event{}, fmt.Errorf("merge template must render a severity")
	}
	name, err := mergeSeverity(level, severity)
	if err != nil {
		return domain.Event{}, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return domain.Event{}, err
	}
	var event domain.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return domain.Event{}, fmt.Errorf("invalid merge event fields")
	}
	event.BKTenantID = decision.TenantID
	event.EventSourceID = source.ID
	event.EventSourceVersion = source.Version
	event.Fingerprint = ParentFingerprint(decision.TenantID, decision.Policy.ID, decision.MemberIDs)
	event.SourceEventID = decision.ID
	event.SourceAlertID = event.Fingerprint
	event.EventID, err = domain.GenerateEventID(event.BKTenantID, event.EventSourceID, event.SourceEventID, decision.FrozenAt)
	if err != nil {
		return domain.Event{}, err
	}
	event.OccurredAt = decision.FrozenAt
	event.ProducedAt = decision.FrozenAt
	event.ReceivedAt = decision.FrozenAt
	event.CreateAt = decision.FrozenAt
	event.Evaluations = []domain.EventEvaluation{{Severity: name, Action: domain.EventActionTriggered}}
	tags := slices.Clone(compiled.Merge.AlarmTags)
	slices.Sort(tags)
	event.MergeOrigin = &domain.MergeOrigin{OperationID: decision.ID, WindowID: decision.WindowID, Policy: domain.PolicyVersion{ID: decision.Policy.ID, Version: decision.Policy.Version, Digest: decision.Policy.Compiled.Digest}, MembersDigest: MembersDigest(decision.MemberIDs), MemberCount: len(decision.MemberIDs), AlarmTags: tags}
	if event.ExtraData == nil {
		event.ExtraData = domain.JSONObject{}
	}
	// 父事件不继承任意成员扩展；仅为显式模板输出保留已声明的同名 KAC 自定义字段。
	// KAC 发布的自定义映射固定为 extra_data 同名键，目录随 prepared Event 冻结。
	customFields := []string{}
	for _, field := range compiled.Merge.Template {
		mapping, ok := compiled.Common.FieldMappings[field.Key]
		if !ok {
			continue
		}
		target, err := jsonpath.ParseTarget(mapping.Path)
		if err != nil {
			return domain.Event{}, err
		}
		parts := target.Parts()
		if len(parts) == 2 && parts[0] == "extra_data" && parts[1] == field.Key {
			customFields = append(customFields, field.Key)
		}
	}
	if len(customFields) > 0 {
		slices.Sort(customFields)
		event.ExtraData["__kac_custom_fields"], err = json.Marshal(customFields)
		if err != nil {
			return domain.Event{}, err
		}
	}
	event.ExtraData["source_id"] = json.RawMessage(`"builtin_alarm_merge"`)
	event.ExtraData["source_name"] = json.RawMessage(`"告警合并"`)
	event.ExtraData["source_alarm_status"] = json.RawMessage(`"firing"`)
	event, err = event.Normalize()
	if err != nil {
		return domain.Event{}, err
	}
	if err := domain.ValidateNormalizedNewEvent(event); err != nil {
		return domain.Event{}, err
	}
	return event, ctx.Err()
}

// 同时接受标准等级及当前快照中唯一的 KAC 别名；存在别名碰撞时拒绝猜测。
func mergeSeverity(value string, snapshot runtimeconfig.Snapshot) (string, error) {
	found := ""
	for _, level := range snapshot.Severity.Levels {
		alias, err := snapshot.KACLevel(level.Name)
		if err != nil {
			return "", err
		}
		if value != level.Name && value != alias {
			continue
		}
		if found != "" && found != level.Name {
			return "", fmt.Errorf("ambiguous merge severity")
		}
		found = level.Name
	}
	if found == "" {
		return "", fmt.Errorf("unknown merge severity")
	}
	return found, nil
}

// RenderAndPrepare 从 Journal 重读完整冻结快照，并在创建 Event 前保存完整模板结果。
// 已经准备完成的操作直接返回原结果，不再依赖当前来源/等级配置；存储失败及取消原样返回。
func (j *Journal) RenderAndPrepare(ctx context.Context, tenant, id string, source eventsource.Release, severity runtimeconfig.Snapshot, at time.Time) (StoredDecision, error) {
	current, err := j.Get(ctx, tenant, id)
	if err != nil {
		return StoredDecision{}, err
	}
	if current.Decision.Progress.Phase != "capturing" {
		return current, nil
	}
	members, err := j.Members(ctx, current.Decision)
	if err != nil {
		return StoredDecision{}, err
	}
	event, err := renderParent(ctx, current.Decision, members, source, severity)
	if err != nil {
		return StoredDecision{}, err
	}
	return j.Prepare(ctx, current, event, at)
}

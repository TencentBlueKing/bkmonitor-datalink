package kachook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
)

// RecoveryFields 转换已应用冻结丰富补丁的来源恢复 Event，并补齐内置策略关联投影。
// 调用方负责核对 Event/Alert 关系和恢复动作；此处不读取当前策略、不生成旧查询参数。
func RecoveryFields(event domain.Event, evaluation domain.EventEvaluation, sourceID, sourceName string, catalog []string, level func(string) (string, error)) (map[string]any, error) {
	fields, err := DebugFields(event, evaluation, sourceID, sourceName, catalog, level)
	if err != nil || event.EventSourceID != "built_in_bk" {
		return fields, err
	}
	values, err := decodeEffectiveEnrich(event.Labels, event.ExtraData)
	if err != nil {
		return nil, err
	}
	extra := strategyFieldExtraInfo(event.BKTenantID, values.strategy)
	if sourceExtra, ok := fields["field_extra_info"].(map[string]any); ok {
		extra = mergeExtraInfo(extra, sourceExtra)
	}
	fields["field_extra_info"] = extra
	return fields, nil
}

// DebugFields 只转换来源已有事实；不构造 Alert、不执行丰富或内容生成。
// 编号属于隔离调试身份，不能作为正式告警操作目标；终态输入不推断历史开始时间或处置状态。
func DebugFields(event domain.Event, evaluation domain.EventEvaluation, sourceID, sourceName string, catalog []string, level func(string) (string, error)) (map[string]any, error) {
	severity := evaluation.Severity
	if severity != domain.SeverityAll {
		var err error
		severity, err = level(severity)
		if err != nil {
			return nil, err
		}
	}
	action := map[domain.EventAction]string{domain.EventActionTriggered: "firing", domain.EventActionResolved: "resolved", domain.EventActionClosed: "close"}[evaluation.Action]
	if action == "" {
		return nil, fmt.Errorf("invalid debug action")
	}
	digest := sha256.Sum256([]byte(event.EventID + "\x00" + evaluation.Severity))
	result := map[string]any{
		"alarm_id": "preview-" + hex.EncodeToString(digest[:]), "event_id": event.SourceAlertID,
		"bk_tenant_id": event.BKTenantID, "source_id": sourceID, "source_name": sourceName,
		"name": event.Title, "content": event.Content, "level": severity, "action": action,
		"alarm_time": event.OccurredAt.In(kacTimeZone).Format(kacTimeLayout),
		"object":     firstNonEmpty(event.SubjectName, event.SubjectID), "meta_info": event.SourceEventID,
	}
	// 原生事实只展开已知业务字段，原始 payload 及任意 labels 不作为自定义字段兜底。
	facts := map[string]any{}
	for _, value := range []any{event.Dimensions, event.Labels, event.ExtraData} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var object map[string]any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&object); err != nil {
			return nil, err
		}
		for key, value := range object {
			facts[key] = value
		}
	}
	protected := strings.Fields("alarm_id event_id bk_tenant_id source_id source_name name content level action alarm_time close_time close_reason")
	denied := map[string]bool{}
	for _, field := range protected {
		denied[field] = true
	}
	typ := reflect.TypeFor[kingeye.AlarmMessage]()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		value, exists := facts[name]
		if denied[name] || !exists {
			continue
		}
		if field.Type.Kind() == reflect.String {
			text, err := sourceText(name, value)
			if err != nil {
				return nil, err
			}
			result[name] = text
		} else {
			// 按正式协议类型解码，避免调试接受正式出口不能表示的值。
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			target := reflect.New(field.Type)
			if err := json.Unmarshal(encoded, target.Interface()); err != nil {
				return nil, fmt.Errorf("invalid KAC field %s", name)
			}
			result[name] = value
		}
	}
	if err := NormalizeIntegerFields(result); err != nil {
		return nil, err
	}
	extra := map[string]any{}
	for name, raw := range event.ExtraData {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		extra[name] = value
	}
	items := make([]any, len(catalog))
	for i, name := range catalog {
		items[i] = name
	}
	encoded := map[string]json.RawMessage{}
	if err := projectCatalog(encoded, extra, items); err != nil {
		return nil, err
	}
	for name, raw := range encoded {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		result[name] = value
	}
	return result, nil
}

func sourceText(field string, value any) (string, error) {
	switch value.(type) {
	case nil, string, bool, json.Number:
		return scalarText(value), nil
	default:
		return "", fmt.Errorf("KAC legacy field %s requires scalar value", field)
	}
}

// NormalizeIntegerFields 与正式兼容写入共用 Integer 字段的空值和精度规则。
// 仅处理已有字段；不为调试补齐来源未提供的数据。
func NormalizeIntegerFields(fields map[string]any) error {
	for _, key := range []string{"bk_biz_id", "bk_cloud_id", "bk_service_id"} {
		value, exists := fields[key]
		if !exists {
			continue
		}
		text := scalarText(value)
		if text == "" {
			fields[key] = nil
			continue
		}
		n, err := strconv.ParseInt(text, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid KAC integer %s", key)
		}
		fields[key] = n
	}
	return nil
}

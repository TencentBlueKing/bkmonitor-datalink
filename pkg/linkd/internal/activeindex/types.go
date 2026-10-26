// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package activeindex 维护限流规则的活跃字段分组集合，供 Cleaner 软限流及外部成员查询共享。
// 主流程尽力增量更新，后台只修复差异；缓存故障不得阻断告警生命周期。
package activeindex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
)

// Row 是来源活跃告警的窄投影，使用存储边界的原始字段类型。
type Row = store.ActiveIndexRow

// RepairCandidate 保存待修复的分组成员，只在单次修复调用中使用。
// GroupKey 和通知内容来自规则，不持久化独立的反向索引。
type RepairCandidate struct {
	Row
	GroupKey      string
	NotifyChannel string
	NotifyMessage string
}

// AlertRow 提取 opening Event 的原始归组字段，不应用丰富补丁。
func AlertRow(a domain.Alert) Row {
	return Row{BKTenantID: a.BKTenantID, EventSourceID: a.EventSourceID, AlertID: a.AlertID, Fingerprint: a.Fingerprint, Labels: a.Labels, Dimensions: a.Dimensions, SubjectSystem: a.SubjectSystem, SubjectType: a.SubjectType, SubjectID: a.SubjectID}
}

// EventRow 仅用于 Cleaner 判定，尚无 AlertID。
func EventRow(e domain.Event) Row {
	return Row{BKTenantID: e.BKTenantID, EventSourceID: e.EventSourceID, Fingerprint: e.Fingerprint, Labels: e.Labels, Dimensions: e.Dimensions, SubjectSystem: e.SubjectSystem, SubjectType: e.SubjectType, SubjectID: e.SubjectID}
}

// Query 限定存储扫描字段与单页预算。
type Query = store.ActiveIndexQuery

// Reader 是只读存储投影端口，不引入配置或运行时依赖。
type Reader = store.ActiveIndexReader

// Observer 使用低基数操作和结果记录缓存降级、跳过规则与拦截。
type Observer interface {
	ObserveActiveAlertCache(context.Context, string, string, int)
}

type group struct {
	Key     string
	Channel string
	Message string
}

func scalar(v domain.Scalar) string {
	if s, ok := v.StringValue(); ok {
		return s
	}
	if n, ok := v.NumberValue(); ok {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	if b, ok := v.BoolValue(); ok {
		return strconv.FormatBool(b)
	}
	return ""
}

func field(r Row, path string) string {
	switch path {
	case "bk_tenant_id":
		return r.BKTenantID
	case "event_source_id":
		return r.EventSourceID
	case "subject_system":
		return r.SubjectSystem
	case "subject_type":
		return r.SubjectType
	case "subject_id":
		return r.SubjectID
	}
	if k, ok := strings.CutPrefix(path, "labels."); ok {
		return scalar(r.Labels[k])
	}
	if k, ok := strings.CutPrefix(path, "dimensions."); ok {
		return scalar(r.Dimensions[k])
	}
	return ""
}

func encode(v string) string { return base64.RawURLEncoding.EncodeToString([]byte(v)) }

// groupKey 是 Cleaner 热路径，只在联合分组时编码数组，不构造通知或诊断 map。
func (s *Store) groupKey(r Row, rule config.ActiveAlertLimitRule) (string, bool) {
	prefix := rule.KeyPrefix
	if prefix == "" {
		prefix = s.namespace + ":rule:" + rule.Name
	}
	key := prefix + ":" + r.BKTenantID
	switch len(rule.GroupBy) {
	case 0:
		return key, true
	case 1:
		value := field(r, rule.GroupBy[0])
		if value == "" {
			return "", false
		}
		return key + ":" + value, true
	default:
		values := make([]string, len(rule.GroupBy))
		for i, path := range rule.GroupBy {
			values[i] = field(r, path)
			if values[i] == "" {
				return "", false
			}
		}
		encoded, _ := json.Marshal(values)
		return key + ":" + string(encoded), true
	}
}

func groupFields(r Row, rule config.ActiveAlertLimitRule) map[string]string {
	fields := make(map[string]string, len(rule.GroupBy))
	for _, path := range rule.GroupBy {
		fields[path] = field(r, path)
	}
	return fields
}

func (s *Store) ruleGroup(r Row, rule config.ActiveAlertLimitRule) (group, bool) {
	key, ok := s.groupKey(r, rule)
	if !ok {
		return group{}, false
	}
	g := group{Key: key}
	if n := rule.Notify; n != nil {
		payload := map[string]string{}
		for name, path := range n.Fields {
			payload[name] = field(r, path)
		}
		encoded, _ := json.Marshal(payload)
		g.Channel = n.Channel
		g.Message = string(encoded)
	}
	return g, true
}

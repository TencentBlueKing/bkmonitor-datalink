// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// ErrNotAegis 表示合法 JSON 未包含 Aegis envelope 标记，接入层可据此选择 OTLP。
// JSON 语法错误或已识别 Aegis 的字段错误不会返回此错误。
var ErrNotAegis = errors.New("not an Aegis V2 payload")

func decodePayload(buf []byte) (collectPayload, []d2Record, error) {
	var payload collectPayload
	if err := json.Unmarshal(buf, &payload); err != nil {
		return payload, nil, err
	}
	if payload.Topic == "" && payload.Scheme == "" && len(payload.D2) == 0 {
		return payload, nil, ErrNotAegis
	}
	var records []d2Record
	if len(payload.D2) > 0 {
		if err := json.Unmarshal(payload.D2, &records); err != nil {
			return payload, nil, err
		}
	}
	return payload, records, nil
}

// 以下类型只描述 Aegis 输入协议，不依赖 BK-RUM 的事件分类或属性命名。
type collectPayload struct {
	Topic  string          `json:"topic"`
	Bean   clientBean      `json:"bean"`
	Scheme string          `json:"scheme"`
	D2     json.RawMessage `json:"d2"`
	Ext    json.RawMessage `json:"ext"`
}

type clientBean struct {
	Version  string `json:"version"`
	AID      string `json:"aid"`
	UID      string `json:"uid"`
	Env      string `json:"env"`
	Platform string `json:"platform"`
	NetType  string `json:"netType"`
	VP       string `json:"vp"`
	SR       string `json:"sr"`
	Referer  string `json:"referer"`
}

type d2Record struct {
	Fields  d2Fields    `json:"fields"`
	Message []d2Message `json:"message"`
}

func (r *d2Record) UnmarshalJSON(data []byte) error {
	type plain struct {
		Fields  json.RawMessage   `json:"fields"`
		Message []json.RawMessage `json:"message"`
	}
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var fs string
	if err := json.Unmarshal(p.Fields, &fs); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(fs), &r.Fields); err != nil {
		return err
	}
	r.Message = make([]d2Message, 0, len(p.Message))
	for _, raw := range p.Message {
		var msg d2Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			return err
		}
		// 空占位消息没有事件内容，不能仅凭 fields 上下文生成业务 Span。
		if len(msg.raw) == 0 {
			continue
		}
		r.Message = append(r.Message, msg)
	}
	return nil
}

type d2Fields struct {
	Level   string      `json:"level"`
	Plugin  string      `json:"plugin"`
	Type    string      `json:"type"`
	From    string      `json:"from"`
	Session sessionInfo `json:"session"`
	View    viewInfo    `json:"view"`
	Action  actionInfo  `json:"action"`
}

type sessionInfo struct {
	ID        string `json:"id"`
	HasReplay *bool  `json:"has_replay"`
}

type actionInfo struct {
	ID               string `json:"id"`
	Timestamp        int64  `json:"timestamp"`
	ActionType       string `json:"action_type"`
	ActionName       string `json:"action_name"`
	ActionTargetName string `json:"action_target_name"`
}

type viewInfo struct {
	ID          string `json:"id"`
	ViewName    string `json:"view_name"`
	Referrer    string `json:"referrer"`
	LoadingType string `json:"loading_type"`
	ViewURL     string `json:"view_url"`
}

type d2Message struct {
	Msg       string `json:"msg"`
	Timestamp int64  `json:"timestamp"`
	raw       map[string]any
}

func (m *d2Message) UnmarshalJSON(data []byte) error {
	// data 始终是 JSON 字符串，解出内层对象
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s == "" {
		return nil
	}
	raw := make(map[string]any)
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return err
	}
	*m = d2Message{Msg: stringField(raw, "msg"), Timestamp: intField(raw, "timestamp"), raw: raw}
	return nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intField(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

func extractFloat64(objs map[string]any, key string) (float64, bool) {
	var value float64
	switch n := objs[key].(type) {
	case float64:
		value = n
	case int64:
		value = float64(n)
	case int:
		value = float64(n)
	case string:
		var err error
		value, err = strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return value, !math.IsNaN(value) && !math.IsInf(value, 0)
}

func extractInt64(objs map[string]any, key string) (int64, bool) {
	if n, ok := objs[key].(int64); ok {
		return n, true
	}
	n, ok := extractFloat64(objs, key)
	if !ok || n < math.MinInt64 || n >= math.MaxInt64 || math.Trunc(n) != n {
		return 0, false
	}
	return int64(n), true
}

func extractBool(objs map[string]any, key string) (bool, bool) {
	if objs == nil {
		return false, false
	}
	v, ok := objs[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// firstNonEmptyString 返回 objs 中第一个非空字符串值，按 keys 顺序查找。
func firstNonEmptyString(objs map[string]any, keys ...string) string {
	if objs == nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := objs[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

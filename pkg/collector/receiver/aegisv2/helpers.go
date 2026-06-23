package aegisv2

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// unmarshalFlexibleJSON 兼容「对象」和「JSON 字符串包裹对象」两种格式。
func unmarshalFlexibleJSON(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		if s == "" {
			return nil
		}
		return json.Unmarshal([]byte(s), dst)
	}
	return json.Unmarshal(trimmed, dst)
}

func upsertString(attrs pcommon.Map, key, value string) {
	if value != "" {
		attrs.UpsertString(key, value)
	}
}

func ensureDefaultServiceName(attrs pcommon.Map) {
	if value, ok := attrs.Get("service.name"); ok && value.AsString() != "" {
		return
	}
	attrs.UpsertString("service.name", defaultServiceName)
}

func upsertNonZeroInt(attrs pcommon.Map, key string, value int64) {
	if value != 0 {
		attrs.UpsertInt(key, value)
	}
}

func upsertFlattenedMap(attrs pcommon.Map, prefix string, objs map[string]any) {
	upsertFlattenedMapWithAllowlist(attrs, prefix, objs, nil)
}

// upsertFlattenedMapWithAllowlist 提供可选的顶层字段白名单控制。
// allowlist 为 nil 或空时，行为与 upsertFlattenedMap 完全一致。
func upsertFlattenedMapWithAllowlist(attrs pcommon.Map, prefix string, objs map[string]any, allowlist map[string]struct{}) {
	if objs == nil {
		return
	}
	for key, value := range objs {
		if len(allowlist) > 0 {
			if _, ok := allowlist[key]; !ok {
				continue
			}
		}
		upsertAny(attrs, prefix+"."+key, value)
	}
}

func extractFloat64(objs map[string]any, key string) (float64, bool) {
	if objs == nil {
		return 0, false
	}
	v, ok := objs[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func extractInt64(objs map[string]any, key string) (int64, bool) {
	if objs == nil {
		return 0, false
	}
	v, ok := objs[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return int64(f), true
		}
	}
	return 0, false
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

// upsertAny 将任意 JSON 值写入 OTel 属性 Map：
// float64 无小数部分存为 int64；[]any 序列化为 JSON 字符串；
// map[string]any 递归展开为点号键（超出 maxUpsertDepth 后整体序列化）。
func upsertAny(attrs pcommon.Map, key string, value any) {
	upsertAnyDepth(attrs, key, value, 0)
}

func upsertAnyDepth(attrs pcommon.Map, key string, value any, depth int) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		upsertString(attrs, key, v)
	case bool:
		attrs.UpsertBool(key, v)
	case float64:
		if float64(int64(v)) == v {
			attrs.UpsertInt(key, int64(v))
			return
		}
		attrs.UpsertDouble(key, v)
	case []any:
		if len(v) == 0 {
			return
		}
		if b, err := json.Marshal(v); err == nil {
			attrs.UpsertString(key, string(b))
		}
	case map[string]any:
		if depth >= maxUpsertDepth {
			if b, err := json.Marshal(v); err == nil {
				attrs.UpsertString(key, string(b))
			}
			return
		}
		for nestedKey, nestedValue := range v {
			upsertAnyDepth(attrs, key+"."+nestedKey, nestedValue, depth+1)
		}
	default:
		if b, err := json.Marshal(v); err == nil {
			attrs.UpsertString(key, string(b))
		}
	}
}

func millisToTimestamp(ms int64, fallback pcommon.Timestamp) pcommon.Timestamp {
	if ms <= 0 {
		return fallback
	}
	return pcommon.NewTimestampFromTime(time.UnixMilli(ms))
}

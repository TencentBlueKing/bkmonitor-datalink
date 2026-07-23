package aegisv2

import (
	"encoding/json"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

const (
	attrEventTimestamp = "event.timestamp"
	attrAegisExtPrefix = "aegisv2.ext"
	attrLink           = "link"

	spanEventAegisFallback = "aegisv2.event"
	spanNameBrowserVital   = "browser.web_vital"

	defaultFlattenMaxDepth = 10
	msgKeyDuration         = "duration"
)

type flattenConfig struct {
	allowlist map[string]struct{}
	maxDepth  int
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

func putCommonResourceAttrs(attrs pcommon.Map, payload collectPayload, sessionID string) {
	ensureDefaultServiceName(attrs)
	upsertString(attrs, "aegisv2.topic", payload.Topic)
	upsertString(attrs, "aegisv2.scheme", payload.Scheme)
	upsertString(attrs, "version", payload.Bean.Version)
	upsertString(attrs, "aid", payload.Bean.AID)
	upsertString(attrs, "env", payload.Bean.Env)
	upsertString(attrs, "platform", payload.Bean.Platform)
	upsertString(attrs, "netType", payload.Bean.NetType)
	upsertString(attrs, "vp", payload.Bean.VP)
	upsertString(attrs, "sr", payload.Bean.SR)
	upsertString(attrs, "session.id", sessionID)
}

func putCollectorScope(scope pcommon.InstrumentationScope, version string) {
	scope.SetName("aegisv2.collect")
	if version != "" {
		scope.SetVersion(version)
	}
}

func recordPageURL(record d2Record) string {
	if record.Fields.View.ViewURL != "" {
		return record.Fields.View.ViewURL
	}
	return record.Fields.From
}

func upsertNonZeroInt(attrs pcommon.Map, key string, value int64) {
	if value != 0 {
		attrs.UpsertInt(key, value)
	}
}

func upsertFlattenedMap(attrs pcommon.Map, prefix string, objs map[string]any) {
	upsertFlattenedMapWithConfig(attrs, prefix, objs, flattenConfig{maxDepth: defaultFlattenMaxDepth})
}

// upsertFlattenedMapWithAllowlist 提供可选的顶层字段白名单控制。
// allowlist 为 nil 或空时，行为与 upsertFlattenedMap 完全一致。
func upsertFlattenedMapWithAllowlist(attrs pcommon.Map, prefix string, objs map[string]any, allowlist map[string]struct{}) {
	upsertFlattenedMapWithConfig(attrs, prefix, objs, flattenConfig{
		allowlist: allowlist,
		maxDepth:  defaultFlattenMaxDepth,
	})
}

func upsertFlattenedMapWithConfig(attrs pcommon.Map, prefix string, objs map[string]any, cfg flattenConfig) {
	if objs == nil {
		return
	}
	if cfg.maxDepth <= 0 {
		cfg.maxDepth = defaultFlattenMaxDepth
	}
	for key, value := range objs {
		if len(cfg.allowlist) > 0 {
			if _, ok := cfg.allowlist[key]; !ok {
				continue
			}
		}
		upsertAnyWithMaxDepth(attrs, prefix+"."+key, value, cfg.maxDepth)
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
// map[string]any 递归展开为点号键（超出 maxDepth 后整体 JSON 序列化）。
func upsertAny(attrs pcommon.Map, key string, value any) {
	upsertAnyWithMaxDepth(attrs, key, value, defaultFlattenMaxDepth)
}

func upsertAnyWithMaxDepth(attrs pcommon.Map, key string, value any, maxDepth int) {
	if maxDepth <= 0 {
		maxDepth = defaultFlattenMaxDepth
	}
	upsertAnyDepth(attrs, key, value, 0, maxDepth)
}

func upsertAnyDepth(attrs pcommon.Map, key string, value any, depth, maxDepth int) {
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
		if depth >= maxDepth {
			if b, err := json.Marshal(v); err == nil {
				attrs.UpsertString(key, string(b))
			}
			return
		}
		for nestedKey, nestedValue := range v {
			upsertAnyDepth(attrs, key+"."+nestedKey, nestedValue, depth+1, maxDepth)
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

// sanitizePayload attempts to fix common malformed payload issues
// (e.g., leading delimiters like comma, which can happen with certain proxies or batching)
func sanitizePayload(bs []byte) []byte {
	if len(bs) == 0 {
		return bs
	}
	start := 0
	for start < len(bs) {
		c := bs[start]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' {
			start++
		} else {
			break
		}
	}
	if start > 0 && start < len(bs) {
		return bs[start:]
	}
	return bs
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

func boolField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	collectorPresentUnit int64 = 1 << iota
	collectorPresentDescription
	collectorPresentInterval
	collectorPresentUsage
	collectorPresentMask = collectorPresentUnit | collectorPresentDescription | collectorPresentInterval | collectorPresentUsage
)

// DecodeMessage accepts direct report arrays and Collector data[].dimension envelopes.
// Invalid records are counted separately so valid siblings can still be persisted.
func DecodeMessage(data []byte) ([]Record, map[string]int, error) {
	records, dropped, _, err := DecodeMessageWithDiagnostic(data)
	return records, dropped, err
}

// DecodeMessageWithDiagnostic also returns the first invalid record's validation
// reason, without retaining or exposing the record payload.
func DecodeMessageWithDiagnostic(data []byte) ([]Record, map[string]int, string, error) {
	data = bytes.TrimSpace(data)
	var raw []json.RawMessage
	envelope := len(data) > 0 && data[0] == '{'
	if envelope {
		var body struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, nil, "", err
		}
		if body.Data == nil {
			return nil, nil, "", fmt.Errorf("missing Collector data array")
		}
		raw = body.Data
	} else if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, "", err
	} else if raw == nil {
		return nil, nil, "", fmt.Errorf("expected a report array")
	}
	records := make([]Record, 0, len(raw))
	dropped := make(map[string]int)
	firstInvalidReason := ""
	for _, item := range raw {
		var timestamp json.RawMessage
		if envelope {
			// Collector places the event timestamp beside dimension, not inside it.
			var point struct {
				Dimension json.RawMessage `json:"dimension"`
				Timestamp json.RawMessage `json:"timestamp"`
			}
			if json.Unmarshal(item, &point) != nil {
				dropped["not_metadata"]++
				continue
			}
			item = point.Dimension
			timestamp = point.Timestamp
		}
		r, err := parseRecord(item, envelope, timestamp)
		if err != nil {
			dropped["invalid_record"]++
			if firstInvalidReason == "" {
				firstInvalidReason = err.Error()
			}
			continue
		}
		records = append(records, r)
	}
	return records, dropped, firstInvalidReason, nil
}

// parseRecord validates one dimension and computes the stable scope and value
// identities used by both the batch processor and MySQL uniqueness keys.
func parseRecord(data []byte, labels bool, timestamp json.RawMessage) (Record, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Record{}, err
	}
	get := func(name string) json.RawMessage {
		if labels {
			name = strings.TrimPrefix(name, "__")
		}
		return fields[name]
	}
	text := func(name string) (string, error) {
		if bytes.Equal(bytes.TrimSpace(get(name)), []byte("null")) {
			return "", fmt.Errorf("%s must be a string", name)
		}
		var s string
		err := json.Unmarshal(get(name), &s)
		return sanitize(s), err
	}
	number := func(name string) (int64, error) {
		b := get(name)
		if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		if len(b) > 0 && b[0] == '"' {
			var s string
			if err := json.Unmarshal(b, &s); err != nil {
				return 0, err
			}
			return strconv.ParseInt(s, 10, 64)
		}
		var n int64
		err := json.Unmarshal(b, &n)
		return n, err
	}
	var r Record
	n, err := number("dataid")
	if err != nil || n <= 0 || n > math.MaxInt32 {
		return r, fmt.Errorf("invalid dataid")
	}
	r.DataID = uint(n)
	if len(get("bk_biz_id")) > 0 {
		n, err := number("bk_biz_id")
		if err != nil || n < 0 || n > math.MaxInt32 {
			return r, fmt.Errorf("invalid bk_biz_id")
		}
		r.BizID = int(n)
	}
	if len(get("bk_tenant_id")) > 0 {
		if r.TenantID, err = text("bk_tenant_id"); err != nil {
			return r, err
		}
	}
	for _, f := range []struct {
		name string
		dst  *string
		max  int
	}{
		{"__scope_type", &r.ScopeType, 32}, {"__metric", &r.FieldName, 255},
		{"__name", &r.OriginalName, 255}, {"__original_type", &r.OriginalType, 16}, {"__type", &r.MetricType, 16},
	} {
		*f.dst, err = text(f.name)
		if err != nil || *f.dst == "" || utf8.RuneCountInString(*f.dst) > f.max {
			return r, fmt.Errorf("invalid %s", f.name)
		}
	}
	rawScope, err := text("__scope")
	if err != nil {
		return r, err
	}
	r.Labels, r.ScopeHash, err = decodeScope(r.ScopeType, rawScope)
	if err != nil {
		return r, err
	}
	r.RawScope = rawScope
	var presentMask *int64
	if labels {
		// Collector envelopes use the present mask to distinguish an omitted
		// metadata field from a field explicitly reported as empty.
		n, e := number("present")
		if e != nil || n < 0 || n&^collectorPresentMask != 0 {
			return r, fmt.Errorf("invalid present mask")
		}
		presentMask = &n
	}
	meta := make(map[string]any)
	if presentMask == nil || *presentMask&collectorPresentInterval != 0 {
		b := get("__interval")
		if len(b) > 0 && !bytes.Equal(b, []byte("null")) {
			n, err := number("__interval")
			if err != nil || n <= 0 {
				return r, fmt.Errorf("invalid interval")
			}
			meta["interval"] = n
		}
	}
	for _, field := range []struct {
		name string
		bit  int64
	}{
		{"unit", collectorPresentUnit},
		{"description", collectorPresentDescription},
		{"usage", collectorPresentUsage},
	} {
		if presentMask != nil && *presentMask&field.bit == 0 {
			continue
		}
		b := get("__" + field.name)
		if len(b) == 0 || bytes.Equal(b, []byte("null")) {
			continue
		}
		s, err := text("__" + field.name)
		if err != nil {
			return r, err
		}
		meta[field.name] = s
	}
	if b := get("__temporality"); len(b) > 0 && !bytes.Equal(b, []byte("null")) {
		s, err := text("__temporality")
		if err != nil {
			return r, err
		}
		meta["temporality"] = s
	}
	r.Meta = canonical(meta)
	r.Fingerprint = digest(canonical(map[string]any{
		"original_name": r.OriginalName, "original_type": r.OriginalType, "metric_type": r.MetricType, "meta": meta,
	}))
	if len(bytes.TrimSpace(timestamp)) == 0 || bytes.Equal(bytes.TrimSpace(timestamp), []byte("null")) {
		timestamp = get("timestamp")
	}
	r.TimestampMS, r.HasTimestamp, err = parseTimestamp(timestamp)
	if err != nil {
		return r, fmt.Errorf("invalid timestamp: %w", err)
	}
	return r, nil
}

func parseTimestamp(raw []byte) (int64, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, false, nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, false, err
		}
		timestamp, err := strconv.ParseInt(text, 10, 64)
		return timestamp, true, err
	}
	var timestamp int64
	if err := json.Unmarshal(raw, &timestamp); err != nil {
		return 0, false, err
	}
	return timestamp, true, nil
}

// 规范标签串 = 标签按键名升序，每个写成 k:v，以 || 连接
// scope_hash = MD5(scope_type + 规范标签串)
// scope 11   = MD5("APM" + "scope_name:http||service_name:checkout")
func decodeScope(kind, raw string) (string, string, error) {
	labels := make(map[string]string)
	if kind == "APM" {
		parts := strings.Split(raw, "-")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("APM scope must contain one unescaped separator")
		}
		for i, name := range []string{"service_name", "scope_name"} {
			var out strings.Builder
			for pos := 0; pos < len(parts[i]); pos++ {
				if parts[i][pos] != '%' {
					out.WriteByte(parts[i][pos])
					continue
				}
				if pos+2 >= len(parts[i]) {
					return "", "", fmt.Errorf("invalid scope escape")
				}
				switch parts[i][pos+1 : pos+3] {
				case "2D":
					out.WriteByte('-')
				case "25":
					out.WriteByte('%')
				default:
					return "", "", fmt.Errorf("invalid scope escape")
				}
				pos += 2
			}
			labels[name] = out.String()
		}
	} else if kind != "DEFAULT" || raw != "" {
		labels["scope"] = raw
	}
	keys := make([]string, 0, len(labels))
	for key, value := range labels {
		if value == "" {
			return "", "", fmt.Errorf("empty scope label %q", key)
		}
		if strings.Contains(value, "||") {
			return "", "", fmt.Errorf("invalid scope delimiter")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+labels[k])
	}
	// 按 Metadata 约定，scope 类型与按键排序后的标签串共同构成稳定身份。
	return canonical(labels), digest(kind + strings.Join(parts, "||")), nil
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 0xffff {
			return utf8.RuneError
		}
		return r
	}, s)
}

func canonical(value any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encoding/json sorts map keys; disabling HTML escaping keeps the canonical
	// bytes stable for fingerprints shared with Metadata.
	_ = enc.Encode(value)
	return strings.TrimSuffix(buf.String(), "\n")
}

// digest is an identity checksum for protocol compatibility, not a security hash.
func digest(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }

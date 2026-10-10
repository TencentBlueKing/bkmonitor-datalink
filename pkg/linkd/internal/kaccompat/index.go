// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	_ "embed"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"linkd/internal/projection"
)

// mappingSnapshot 来自 Kingeye 1874066858 的 AlarmEventDocument/AlarmEvent，保留原字段和动态模板。
//
//go:embed alarm_event_mapping.json
var mappingSnapshot []byte

func alarmMapping() map[string]any {
	var value map[string]any
	if json.Unmarshal(mappingSnapshot, &value) != nil {
		return nil
	}
	return value
}

func (c *Client) settings() map[string]any {
	es := c.config.Elasticsearch
	return map[string]any{
		"lifecycle":        map[string]any{"name": c.config.AlarmEventIndex + "_policy", "rollover_alias": c.config.AlarmEventIndex},
		"number_of_shards": es.NumberOfShards, "number_of_replicas": *es.NumberOfReplicas, "max_result_window": es.MaxResultWindow, "max_terms_count": 196605,
		"mapping":  map[string]any{"total_fields": map[string]any{"limit": es.TotalFieldsLimit}},
		"analysis": map[string]any{"analyzer": map[string]any{"split_by_whitespace_analyzer": map[string]any{"type": "custom", "char_filter": []string{"split_by_whitespace_analyzer"}, "tokenizer": "whitespace", "filter": []string{"lowercase"}}}, "char_filter": map[string]any{"split_by_whitespace_analyzer": map[string]any{"pattern": "(.+?)", "type": "pattern_replace", "replacement": "$1 "}}},
	}
}

// Maintain 幂等维护 KAC 模板、ILM、引导 alias 和独立同步元数据索引。
// 已有物理索引仅补齐缺失字段，不重建或回填历史；已有字段冲突仍明确失败。
func (c *Client) Maintain(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			c.ready.Store(false)
		}
	}()
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	alias := c.config.AlarmEventIndex
	policy := map[string]any{"policy": map[string]any{"phases": map[string]any{"hot": map[string]any{"actions": map[string]any{"rollover": map[string]any{"max_size": "30gb", "max_age": "60d"}}}}}}
	if err := c.request(call, http.MethodPut, "/_ilm/policy/"+url.PathEscape(alias+"_policy"), nil, policy, nil); err != nil {
		return classify(err)
	}
	var templates map[string]struct {
		Mappings map[string]any `json:"mappings"`
		Settings map[string]any `json:"settings"`
		Patterns []string       `json:"index_patterns"`
	}
	err = c.request(call, http.MethodGet, "/_template/"+url.PathEscape(alias), nil, nil, &templates)
	if hasStatus(err, 404) {
		body := map[string]any{"index_patterns": []string{alias + "*"}, "mappings": alarmMapping(), "settings": c.settings()}
		if err = c.request(call, http.MethodPut, "/_template/"+url.PathEscape(alias), nil, body, nil); err != nil {
			return classify(err)
		}
	} else if err != nil {
		return classify(err)
	} else {
		tmpl, ok := templates[alias]
		if !ok || len(tmpl.Patterns) != 1 || tmpl.Patterns[0] != alias+"*" || !compatibleMapping(tmpl.Mappings) {
			return projection.Failure{Code: "response_invalid"}
		}
		settings, _ := tmpl.Settings["index"].(map[string]any)
		lifecycle, _ := settings["lifecycle"].(map[string]any)
		expectedAnalysis := c.settings()["analysis"]
		raw, _ := json.Marshal(settings["analysis"])
		expected, _ := json.Marshal(expectedAnalysis)
		if lifecycle["name"] != alias+"_policy" || lifecycle["rollover_alias"] != alias || string(raw) != string(expected) {
			return projection.Failure{Code: "response_invalid"}
		}
	}
	_, err = c.writeIndex(call)
	if hasStatus(err, 404) {
		body := map[string]any{"aliases": map[string]any{alias: map[string]any{"is_write_index": true}}}
		err = c.request(call, http.MethodPut, "/"+url.PathEscape(alias+"-000001"), nil, body, nil)
		if err != nil {
			// 并发维护可能已创建；只在重新读取到有效 write alias 时接受。
			if _, check := c.writeIndex(call); check != nil {
				return classify(err)
			}
		}
	} else if err != nil {
		return classify(err)
	}
	var indices map[string]struct {
		Mappings map[string]any `json:"mappings"`
	}
	if err = c.request(call, http.MethodGet, "/"+url.PathEscape(alias)+"/_mapping", nil, nil, &indices); err != nil {
		return classify(err)
	}
	if len(indices) == 0 || len(indices) > 512 {
		return projection.Failure{Code: "response_invalid"}
	}
	// 先检查全部索引，避免已知字段冲突时仍修改其他历史索引。
	patches := make(map[string]map[string]any)
	for name, index := range indices {
		fields, ok := missingMappingFields(index.Mappings)
		if !c.physicalIndex(name) || !ok {
			return projection.Failure{Code: "response_invalid"}
		}
		if len(fields) > 0 {
			patches[name] = fields
		}
	}
	for name, fields := range patches {
		// 显式追加定义，避免更新历史文档时动态映射猜错字段类型；不回填文档。
		if err = c.request(call, http.MethodPut, "/"+url.PathEscape(name)+"/_mapping", nil, map[string]any{"properties": fields}, nil); err != nil {
			return classify(err)
		}
	}
	var state any
	err = c.request(call, http.MethodGet, "/"+c.stateIndex+"/_mapping", nil, nil, &state)
	if hasStatus(err, 404) {
		body := map[string]any{"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": *c.config.Elasticsearch.NumberOfReplicas, "hidden": true}, "mappings": map[string]any{"dynamic": "strict", "properties": map[string]any{"state": map[string]any{"type": "object", "enabled": false}}}}
		err = c.request(call, http.MethodPut, "/"+c.stateIndex, nil, body, nil)
		if err != nil {
			if check := c.request(call, http.MethodGet, "/"+c.stateIndex+"/_mapping", nil, nil, &state); check != nil {
				return classify(err)
			}
		}
	} else if err != nil {
		return classify(err)
	}
	c.ready.Store(true)
	return nil
}

// missingMappingFields 只允许缺字段；已有类型、分析器和动态规则必须保持兼容。
func missingMappingFields(actual map[string]any) (map[string]any, bool) {
	props, ok := actual["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	candidate := maps.Clone(actual)
	complete := maps.Clone(props)
	missing := make(map[string]any)
	for name, definition := range alarmMapping()["properties"].(map[string]any) {
		if _, exists := props[name]; !exists {
			missing[name] = definition
			complete[name] = definition
		}
	}
	candidate["properties"] = complete
	return missing, compatibleMapping(candidate)
}

// KAC 原字段必须保持类型及分析器一致；额外业务动态字段不算冲突。
func compatibleMapping(actual map[string]any) bool {
	expected := alarmMapping()
	if expected == nil {
		return false
	}
	props, ok := actual["properties"].(map[string]any)
	if !ok {
		return false
	}
	for key, want := range expected["properties"].(map[string]any) {
		got, ok := props[key].(map[string]any)
		if !ok {
			return false
		}
		for k, v := range want.(map[string]any) {
			// ES 在对象获得 properties 后会省略显式 type=object。
			if k == "type" && v == "object" && got[k] == nil && got["properties"] != nil {
				continue
			}
			if !reflect.DeepEqual(got[k], v) {
				// JSON 解码器的数字表示可以不同，比较其 JSON 值。
				a, _ := json.Marshal(got[k])
				b, _ := json.Marshal(v)
				if string(a) != string(b) {
					return false
				}
			}
		}
	}
	dynamic := actual["dynamic"]
	if dynamic != true && dynamic != "true" {
		return false
	}
	a, _ := json.Marshal(actual["dynamic_templates"])
	b, _ := json.Marshal(expected["dynamic_templates"])
	return string(a) == string(b)
}

func (c *Client) writeIndex(ctx context.Context) (string, error) {
	var result map[string]struct {
		Aliases map[string]struct {
			Write *bool `json:"is_write_index"`
		} `json:"aliases"`
	}
	err := c.request(ctx, http.MethodGet, "/_alias/"+url.PathEscape(c.config.AlarmEventIndex), nil, nil, &result)
	if err != nil {
		return "", err
	}
	if len(result) == 0 || len(result) > 512 {
		return "", projection.Failure{Code: "response_invalid"}
	}
	selected := ""
	for index, entry := range result {
		a, ok := entry.Aliases[c.config.AlarmEventIndex]
		if !ok || !c.physicalIndex(index) {
			return "", projection.Failure{Code: "response_invalid"}
		}
		if a.Write != nil && *a.Write || len(result) == 1 && a.Write == nil {
			if selected != "" {
				return "", projection.Failure{Code: "response_invalid"}
			}
			selected = index
		}
	}
	if selected == "" {
		return "", projection.Failure{Code: "response_invalid"}
	}
	return selected, nil
}

type version struct {
	Seq  int64 `json:"_seq_no"`
	Term int64 `json:"_primary_term"`
}

func (v version) query() url.Values {
	return url.Values{"if_seq_no": {strconv.FormatInt(v.Seq, 10)}, "if_primary_term": {strconv.FormatInt(v.Term, 10)}}
}

func validPhysicalName(name string) bool {
	if name == "" || len(name) > 255 || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if r != '-' && r != '_' && r != '.' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

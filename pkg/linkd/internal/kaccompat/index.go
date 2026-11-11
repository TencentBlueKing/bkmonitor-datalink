// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

//go:embed alarm_event_mapping.json
var mappingSnapshot []byte

func alarmMapping() map[string]any {
	var mapping map[string]any
	_ = json.Unmarshal(mappingSnapshot, &mapping)
	mapping["properties"].(map[string]any)["linkd_disposal_status"] = map[string]any{"type": "keyword"}
	return mapping
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

// Maintain 维护 KAC alias、ILM 和 mapping，并校正模板中的 latest_event_time 声明。
// 只有模板与已有索引均准备成功才放行写入；已有索引的字段类型冲突直接返回错误。
func (c *Client) Maintain(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	alias := c.config.AlarmEventIndex
	policy := map[string]any{"policy": map[string]any{"phases": map[string]any{"hot": map[string]any{"actions": map[string]any{"rollover": map[string]any{"max_size": "30gb", "max_age": "60d"}}}}}}
	if err := c.request(call, http.MethodPut, "/_ilm/policy/"+url.PathEscape(alias+"_policy"), nil, policy, nil); err != nil {
		return err
	}
	var existing map[string]map[string]any
	err := c.request(call, http.MethodGet, "/_template/"+url.PathEscape(alias), nil, nil, &existing)
	if hasStatus(err, 404) {
		body := map[string]any{"index_patterns": []string{alias + "*"}, "mappings": alarmMapping(), "settings": c.settings()}
		if err = c.request(call, http.MethodPut, "/_template/"+url.PathEscape(alias), nil, body, nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if template := existing[alias]; template != nil {
		mappings, _ := template["mappings"].(map[string]any)
		if mappings == nil {
			mappings = map[string]any{}
			template["mappings"] = mappings
		}
		properties, _ := mappings["properties"].(map[string]any)
		if properties == nil {
			properties = map[string]any{}
			mappings["properties"] = properties
		}
		changed := false
		currentProperties := alarmMapping()["properties"].(map[string]any)
		for _, field := range []string{"linkd_disposal_status", "fingerprint"} {
			if properties[field] == nil {
				properties[field] = currentProperties[field]
				changed = true
			}
		}
		// 模板可替换错误声明；只补缺失字段会让 rollover 继承不兼容的类型或格式。
		// 正确声明上的其他选项仍保留，物理索引的类型冲突由后面的 mapping 请求拒绝。
		latest, _ := properties["latest_event_time"].(map[string]any)
		currentLatest := currentProperties["latest_event_time"].(map[string]any)
		if latest["type"] != currentLatest["type"] || latest["format"] != currentLatest["format"] {
			properties["latest_event_time"] = currentLatest
			changed = true
		}
		// 模板可删除退役声明；已有索引 mapping 和历史 _source 保持原样。
		for _, field := range []string{"strategy_config_uid", "strategy_config_version"} {
			if _, exists := properties[field]; exists {
				delete(properties, field)
				changed = true
			}
		}
		if changed {
			if err = c.request(call, http.MethodPut, "/_template/"+url.PathEscape(alias), nil, template, nil); err != nil {
				return err
			}
		}
	}
	if _, err = c.writeIndex(call); hasStatus(err, 404) {
		body := map[string]any{"aliases": map[string]any{alias: map[string]any{"is_write_index": true}}}
		if err = c.request(call, http.MethodPut, "/"+url.PathEscape(alias+"-000001"), nil, body, nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = c.request(call, http.MethodPut, "/"+url.PathEscape(alias)+"/_mapping", nil, map[string]any{"properties": alarmMapping()["properties"]}, nil); err != nil {
		return fmt.Errorf("prepare KAC index mapping: %w", err)
	}
	c.mappingReady.Store(true)
	return nil
}

func (c *Client) writeIndex(ctx context.Context) (string, error) {
	var indices map[string]struct {
		Aliases map[string]struct {
			Write *bool `json:"is_write_index"`
		} `json:"aliases"`
	}
	if err := c.request(ctx, http.MethodGet, "/_alias/"+url.PathEscape(c.config.AlarmEventIndex), nil, nil, &indices); err != nil {
		return "", err
	}
	for index, entry := range indices {
		if alias, ok := entry.Aliases[c.config.AlarmEventIndex]; ok && (alias.Write != nil && *alias.Write || len(indices) == 1 && alias.Write == nil) {
			return index, nil
		}
	}
	return "", fmt.Errorf("KAC alias has no write index")
}

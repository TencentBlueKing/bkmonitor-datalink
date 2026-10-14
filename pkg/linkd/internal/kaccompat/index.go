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

// Maintain 保留原 KAC alias、ILM 和 mapping，只追加当前字段；不创建同步元数据索引。
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
		if properties["linkd_disposal_status"] == nil {
			properties["linkd_disposal_status"] = map[string]any{"type": "keyword"}
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
	return c.request(call, http.MethodPut, "/"+url.PathEscape(alias)+"/_mapping", nil, map[string]any{"properties": alarmMapping()["properties"]}, nil)
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

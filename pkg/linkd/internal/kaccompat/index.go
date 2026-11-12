// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
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
		"number_of_shards": es.NumberOfShards, "number_of_replicas": *es.NumberOfReplicas, "max_result_window": es.MaxResultWindow, "max_terms_count": 196605,
		"mapping":  map[string]any{"total_fields": map[string]any{"limit": es.TotalFieldsLimit}},
		"analysis": map[string]any{"analyzer": map[string]any{"split_by_whitespace_analyzer": map[string]any{"type": "custom", "char_filter": []string{"split_by_whitespace_analyzer"}, "tokenizer": "whitespace", "filter": []string{"lowercase"}}}, "char_filter": map[string]any{"split_by_whitespace_analyzer": map[string]any{"pattern": "(.+?)", "type": "pattern_replace", "replacement": "$1 "}}},
	}
}

// Maintain 维护 KAC 查询 alias、时间桶模板和 mapping，并校正模板中的 latest_event_time 声明。
// 只有模板与已有索引均准备成功才放行写入；已有索引的字段类型冲突直接返回错误。
func (c *Client) Maintain(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	alias := c.config.AlarmEventIndex
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
		changed := removeTemplateLifecycle(template)
		currentProperties := alarmMapping()["properties"].(map[string]any)
		for _, field := range []string{"linkd_disposal_status", "fingerprint"} {
			if properties[field] == nil {
				properties[field] = currentProperties[field]
				changed = true
			}
		}
		// 模板可替换错误声明；只补缺失字段会让新时间桶继承不兼容的类型或格式。
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
	if err = c.request(call, http.MethodGet, "/_alias/"+url.PathEscape(alias), nil, nil, nil); hasStatus(err, 404) {
		if err = c.ensureBucket(call, c.bucketIndex(time.Now())); err != nil {
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

// removeTemplateLifecycle 只移除模板的 ILM 声明；旧索引及其策略保持原样。
// ES 返回的 legacy template settings 可能使用嵌套或扁平格式。
func removeTemplateLifecycle(template map[string]any) bool {
	settings, _ := template["settings"].(map[string]any)
	return removeLifecycleSettings(settings)
}

func removeLifecycleSettings(settings map[string]any) bool {
	changed := false
	for _, key := range []string{"lifecycle", "index.lifecycle.name", "index.lifecycle.rollover_alias", "lifecycle.name", "lifecycle.rollover_alias"} {
		if _, ok := settings[key]; ok {
			delete(settings, key)
			changed = true
		}
	}
	if index, ok := settings["index"].(map[string]any); ok {
		changed = removeLifecycleSettings(index) || changed
	}
	return changed
}

// bucketIndex 与 Linkd 时间桶使用相同的 UTC 周一原点；更新与重试不依赖当前时间。
func (c *Client) bucketIndex(created time.Time) string {
	epoch := time.Date(1970, time.January, 5, 0, 0, 0, 0, time.UTC)
	value := created.UTC()
	date := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	days := int(date.Sub(epoch) / (24 * time.Hour))
	width := c.config.AlarmEventBucketDays
	bucket := days / width
	if days < 0 && days%width != 0 {
		bucket--
	}
	return c.config.AlarmEventIndex + "-" + epoch.AddDate(0, 0, bucket*width).Format("20060102")
}

// ensureBucket 按需准备历史或当前时间桶，支持延迟投影；创建索引时原子加入原查询 alias。
// 不切换旧写别名，不修改旧索引或旧 ILM。新桶没有默认写目标，业务写入使用确定的物理索引。
func (c *Client) ensureBucket(ctx context.Context, index string) error {
	path := "/" + url.PathEscape(index)
	err := c.request(ctx, http.MethodHead, path, nil, nil, nil)
	if err == nil {
		return nil
	}
	if !hasStatus(err, http.StatusNotFound) {
		return err
	}
	body := map[string]any{"aliases": map[string]any{c.config.AlarmEventIndex: map[string]any{"is_write_index": false}}}
	err = c.request(ctx, http.MethodPut, path, nil, body, nil)
	if err != nil {
		// 并发批次可能同时建桶；只接受明确的索引已存在错误，不掩盖 mapping 等其他 400 错误。
		var failure httpFailure
		if errors.As(err, &failure) && failure.errorType == "resource_already_exists_exception" {
			return nil
		}
	}
	return err
}

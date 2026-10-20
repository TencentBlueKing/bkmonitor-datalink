// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/kachook"
)

// WriteResult 按输入顺序返回单条写入结果，Index 供动作批次刷新。
type WriteResult struct {
	Index string
	Err   error
}

// 状态转换是业务更新的一部分：普通 active 保留 KAC 处置，屏蔽/合并保存并恢复原处置状态。
// 不读写版本、水位或摘要；脚本只修改声明的字段，标签保留两侧追加内容。
const updateScript = `
if (ctx.op == 'create') { ctx._source.putAll(params.create); }
def old = ctx._source.status;
boolean blocked = ['shielded','pending_merge','merged_into'].contains(old);
if (params.mode != 'active') {
  if (['shielded','pending_merge','merged_into'].contains(params.mode) && !blocked && old != null) { ctx._source.linkd_disposal_status = old; }
  ctx._source.status = params.mode;
} else if (blocked || old == null) {
  ctx._source.status = ctx._source.linkd_disposal_status != null ? ctx._source.linkd_disposal_status : 'received';
}
ctx._source.putAll(params.fields);
if (ctx._source.tag_info == null) { ctx._source.tag_info = []; }
for (tag in params.tags) { if (!ctx._source.tag_info.contains(tag)) { ctx._source.tag_info.add(tag); } }
`

// WriteBatch 只执行一次批量新建/局部更新；失败由调用方记录，不建立补偿任务。
func (c *Client) WriteBatch(ctx context.Context, alerts []domain.Alert, refresh bool) []WriteResult {
	results := make([]WriteResult, len(alerts))
	ids := make([]string, len(alerts))
	missing := make([]string, 0, len(alerts))
	locations := make(map[string]string, len(alerts))
	for i, a := range alerts {
		ids[i], _ = kachook.AlarmID(a.BKTenantID, a.AlertID)
	}
	c.mu.Lock()
	for _, id := range ids {
		if index := c.locations[id]; index != "" {
			locations[id] = index
		} else {
			missing = append(missing, id)
		}
	}
	c.mu.Unlock()
	if len(missing) > 0 {
		var response struct {
			Hits struct {
				Hits []struct {
					ID    string `json:"_id"`
					Index string `json:"_index"`
				} `json:"hits"`
			} `json:"hits"`
		}
		body := map[string]any{"size": len(missing), "_source": false, "query": map[string]any{"ids": map[string]any{"values": missing}}}
		if err := c.request(ctx, http.MethodPost, "/"+url.PathEscape(c.config.AlarmEventIndex)+"/_search", nil, body, &response); err != nil {
			for i := range results {
				results[i].Err = err
			}
			return results
		}
		for _, hit := range response.Hits.Hits {
			locations[hit.ID] = hit.Index
		}
	}
	writeIndex := ""
	var raw bytes.Buffer
	positions := []int{}
	flush := func() {
		if len(positions) == 0 {
			return
		}
		var response struct {
			Items []map[string]struct {
				Status int    `json:"status"`
				Index  string `json:"_index"`
			} `json:"items"`
		}
		query := url.Values{"refresh": {"false"}}
		if refresh {
			query.Set("refresh", "true")
		}
		err := c.requestRaw(ctx, http.MethodPost, "/_bulk", query, raw.Bytes(), "application/x-ndjson", &response)
		for j, pos := range positions {
			if err != nil {
				results[pos].Err = err
				continue
			}
			if j >= len(response.Items) {
				results[pos].Err = fmt.Errorf("KAC bulk response missing item")
				continue
			}
			for _, item := range response.Items[j] {
				if item.Status >= 300 {
					results[pos].Err = httpFailure{item.Status}
					if item.Status == 404 {
						c.mu.Lock()
						delete(c.locations, ids[pos])
						c.mu.Unlock()
					}
					continue
				}
				results[pos].Index = item.Index
				c.mu.Lock()
				if len(c.locations) >= 100000 {
					clear(c.locations)
				}
				c.locations[ids[pos]] = item.Index
				c.mu.Unlock()
			}
		}
		raw.Reset()
		positions = nil
	}
	for i, a := range alerts {
		index, exists := locations[ids[i]]
		fields, mode, err := c.fields(ctx, a, !exists)
		if err != nil {
			results[i].Err = err
			continue
		}
		tags := fields["tag_info"]
		delete(fields, "tag_info")
		patch := map[string]any{}
		for _, field := range []string{"content", "level", "action", "source_alarm_status", "close_reason", "close_time", "associate_alarm_id", "associate_count"} {
			patch[field] = fields[field]
		}
		params := map[string]any{"fields": patch, "mode": mode, "tags": tags}
		body := map[string]any{"script": map[string]any{"lang": "painless", "source": updateScript, "params": params}}
		if !exists {
			if writeIndex == "" {
				writeIndex, err = c.writeIndex(ctx)
				if err != nil {
					results[i].Err = err
					continue
				}
			}
			index = writeIndex
			params["create"] = fields
			body["scripted_upsert"], body["upsert"] = true, map[string]any{}
		}
		head, _ := json.Marshal(map[string]any{"update": map[string]any{"_index": index, "_id": ids[i]}})
		content, err := json.Marshal(body)
		if err != nil {
			results[i].Err = err
			continue
		}
		size := len(head) + len(content) + 2
		if size > c.config.Delivery.MaxBatchBytes {
			results[i].Err = fmt.Errorf("KAC document exceeds batch byte budget")
			continue
		}
		if raw.Len()+size > c.config.Delivery.MaxBatchBytes {
			flush()
		}
		raw.Write(head)
		raw.WriteByte('\n')
		raw.Write(content)
		raw.WriteByte('\n')
		positions = append(positions, i)
	}
	flush()
	return results
}

// Refresh 供动作发送批次调用一次，使 KAC 的搜索读取能够发现刚写入的文档。
func (c *Client) Refresh(ctx context.Context, indices []string) error {
	if len(indices) == 0 {
		return nil
	}
	return c.request(ctx, http.MethodPost, "/"+strings.Join(indices, ",")+"/_refresh", nil, nil, nil)
}

const kacTimeLayout = "2006-01-02 15:04:05"

var kacZone = time.FixedZone("Asia/Shanghai", 8*3600)

func (c *Client) fields(ctx context.Context, a domain.Alert, create bool) (map[string]any, string, error) {
	fields := map[string]any{}
	if create {
		raw, err := kachook.CompatibilityPayload(a, c.level)
		if err != nil {
			return nil, "", err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&fields); err != nil {
			return nil, "", err
		}
		if err = kachook.NormalizeIntegerFields(fields); err != nil {
			return nil, "", err
		}
		fields["storage_time"] = time.Now().In(kacZone).Format(kacTimeLayout)
		fields["conductor"] = []string{}
		fields["notify_status"] = ""
	}
	level, err := c.level(a.Severity)
	if err != nil {
		return nil, "", err
	}
	fields["level"] = level
	// 持续触发已经更新 Alert.Content；兼容文档必须同步最新正文，包括显式清空。
	// 其他展示/丰富字段仍保留创建快照，不重置 KAC 的处置、通知或工单信息。
	fields["content"] = a.Content
	action := "firing"
	if a.Status == domain.AlertStatusRecovered {
		action = "resolved"
	}
	if a.Status == domain.AlertStatusClosed {
		action = "close"
	}
	fields["action"], fields["source_alarm_status"] = action, action
	fields["close_reason"] = a.EndReason
	if a.EndAt != nil {
		fields["close_time"] = a.EndAt.In(kacZone).Format(kacTimeLayout)
	} else {
		fields["close_time"] = nil
	}
	parents := map[string]bool{}
	members := map[string]bool{}
	if a.Merge != nil && len(a.Merge.RelationIDs) > 0 {
		if c.relations == nil {
			return nil, "", fmt.Errorf("KAC merge relation unavailable")
		}
		for _, id := range a.Merge.RelationIDs {
			relation, e := c.relations.GetMergeRelation(ctx, a.BKTenantID, id)
			if e != nil {
				return nil, "", fmt.Errorf("KAC merge relation unavailable")
			}
			if relation.TenantID != a.BKTenantID || relation.ID != id {
				return nil, "", fmt.Errorf("KAC merge relation scope mismatch")
			}
			if a.Merge.Role == "original" {
				parent, e := kachook.AlarmID(a.BKTenantID, relation.ParentAlertID)
				if e != nil {
					return nil, "", e
				}
				parents[parent] = true
			} else {
				if relation.ParentAlertID != a.AlertID {
					return nil, "", fmt.Errorf("KAC merge relation scope mismatch")
				}
				for _, member := range relation.Members {
					members[member.AlertID] = true
				}
			}
		}
	}
	parentIDs := make([]string, 0, len(parents))
	for id := range parents {
		parentIDs = append(parentIDs, id)
	}
	sort.Strings(parentIDs)
	fields["associate_alarm_id"] = parentIDs
	fields["associate_count"] = len(members)
	tags := make([]any, 0, len(a.PolicyTags))
	for _, tag := range a.PolicyTags {
		tags = append(tags, strconv.FormatInt(tag, 10))
	}
	fields["tag_info"] = tags
	mode := "active"
	switch {
	case a.Status == domain.AlertStatusRecovered:
		mode = "restored"
	case a.Status == domain.AlertStatusClosed:
		mode = "system_closed"
		switch a.EndType {
		case domain.AlertEndTypeUser:
			mode = "closed"
		case domain.AlertEndTypeSource:
			mode = "source_closed"
		}
		// 操作者类别不足以区分自动关闭、自愈和工单，展示状态沿用已冻结的关闭来源。
		// 无主动命令的来源关闭及历史记录继续使用 EndType 的既有投影。
		if a.EndOperation != nil {
			switch a.EndOperation.Source {
			case "auto_policy":
				mode = "auto_closed"
			case "self_heal":
				mode = "autoexecute_closed"
			case "work_order":
				mode = "autoorder_closed"
			}
		}
	case a.Shield.Active:
		mode = "shielded"
	case a.Merge != nil && a.Merge.Role == "original" && len(a.Merge.RelationIDs) > 0:
		mode = "merged_into"
	case a.Merge.Blocking():
		mode = "pending_merge"
	}
	return fields, mode, nil
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/projection"
)

type syncState struct {
	Request        projection.Request `json:"request"`
	Index          string             `json:"index"`
	Fields         map[string]any     `json:"fields"`
	Mode           string             `json:"mode"`
	PreviousMode   string             `json:"previous_mode"`
	DisposalStatus string             `json:"disposal_status"`
	StorageTime    string             `json:"storage_time"`
	Applied        bool               `json:"applied"`
}

type stateDocument struct {
	State syncState `json:"state"`
}

type storedState struct {
	version
	Found  bool          `json:"found"`
	Source stateDocument `json:"_source"`
}

type alarmDocument struct {
	version
	Found  bool           `json:"found"`
	Source map[string]any `json:"_source"`
}

// Send 按冻结快照写入原物理索引并确认搜索可见，元数据索引先保存定位和待应用版本。
// 每次循环先读兼容文档的 CAS 版本，再核对最新同步意图；并发的较新写入会使旧 CAS 失败，
// 旧任务不得重新读取新 seq_no 后绕过意图检查。由此无需向 KAC mapping 增加版本字段。
// 失败可能已写入兼容文档，调用方必须重试同一请求；最多八次 CAS 竞争，之后交给持久重试。
func (c *Client) Send(ctx context.Context, d projection.Destination, q projection.Request) (receipt projection.Receipt, err error) {
	if ctx == nil || d.TargetID != "kac" || q.TargetID != "kac" || q.Validate() != nil {
		return receipt, projection.ErrInvalid
	}
	if !c.ready.Load() {
		return receipt, projection.Failure{Code: "target_unavailable", Retryable: true}
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	defer func() { err = classify(err) }()
	initial, err := c.getState(call, q)
	if hasStatus(err, 404) {
		initial, err = c.initialize(call, q)
	}
	if err != nil {
		return receipt, err
	}
	index := initial.Source.State.Index
	for range 8 {
		// 必须先读文档再读意图，不能交换顺序；KAC 处置写入也受本次文档 CAS 保护。
		document, err := c.getAlarm(call, index, q.AlarmID)
		missing := hasStatus(err, 404)
		if err != nil && !missing {
			return receipt, err
		}
		state, err := c.getState(call, q)
		if err != nil {
			return receipt, err
		}
		current := state.Source.State
		if current.Index != index {
			return receipt, projection.Failure{Code: "revision_conflict"}
		}
		if current.Request.Revision < q.Revision {
			fields, mode, err := c.fields(call, q)
			if err != nil {
				return receipt, err
			}
			next := current
			next.Request = q.Clone()
			next.Fields = fields
			next.Mode = mode
			next.Applied = false
			if err = c.putState(call, q.AlarmID, next, state.version); hasStatus(err, 409) {
				continue
			} else if err != nil {
				return receipt, err
			}
			continue
		}
		if current.Request.Revision == q.Revision && current.Request.ContentHash != q.ContentHash {
			return receipt, projection.Failure{Code: "revision_conflict"}
		}
		if !missing && (document.Source["bk_tenant_id"] != q.TenantID || document.Source["alarm_id"] != q.AlarmID) {
			return receipt, projection.Failure{Code: "revision_conflict"}
		}
		if current.Applied && !missing {
			return receiptFor(current), nil
		}
		status, _ := document.Source["status"].(string)
		if isDisposalStatus(status) && status != current.DisposalStatus {
			current.DisposalStatus = status
			if err = c.putState(call, q.AlarmID, current, state.version); hasStatus(err, 409) {
				continue
			} else if err != nil {
				return receipt, err
			}
			continue
		}
		patch := cloneFields(current.Fields)
		if current.Mode != "active" && (current.Mode != current.PreviousMode || isPolicyStatus(status) && status != current.Mode) {
			patch["status"] = current.Mode
		} else if current.Mode == "active" && (current.PreviousMode != "active" || status == "" || isPolicyStatus(status)) {
			if current.DisposalStatus != "" {
				patch["status"] = current.DisposalStatus
			} else {
				patch["status"] = "received"
			}
		} else if status == "" {
			patch["status"] = current.Mode
		} else {
			// 没有生命周期/策略状态转换时，允许 KAC 继续推进已有处置，不能重置为旧兼容状态。
			patch["status"] = status
		}
		// field_extra_info 中 KAC 可以增加快照/工单链接，不能用初始展示 URL 覆盖整个对象。
		if previous, ok := document.Source["field_extra_info"].(map[string]any); ok {
			patch["field_extra_info"] = mergeFields(previous, patch["field_extra_info"])
		}
		if tags, ok := document.Source["tag_info"].([]any); ok {
			patch["tag_info"] = mergeTags(tags, patch["tag_info"])
		}
		var response version
		if missing {
			if err = c.request(call, http.MethodHead, "/"+url.PathEscape(index), nil, nil, nil); err != nil {
				return receipt, err
			}
			patch["storage_time"] = current.StorageTime
			patch["conductor"] = []string{}
			patch["notify_status"] = ""
			err = c.request(call, http.MethodPut, "/"+url.PathEscape(index)+"/_create/"+url.PathEscape(q.AlarmID), nil, patch, &response)
		} else {
			err = c.request(call, http.MethodPost, "/"+url.PathEscape(index)+"/_update/"+url.PathEscape(q.AlarmID), document.query(), map[string]any{"doc": patch}, &response)
		}
		if hasStatus(err, 409) {
			continue
		}
		if err != nil {
			return receipt, err
		}
		// 同版本重试可能是 update noop；显式 refresh 也覆盖响应丢失后的重复写，不把 GET 可见当作搜索可见。
		var refreshed struct {
			Shards *struct {
				Total  int `json:"total"`
				Failed int `json:"failed"`
			} `json:"_shards"`
		}
		if err = c.request(call, http.MethodPost, "/"+url.PathEscape(index)+"/_refresh", nil, nil, &refreshed); err != nil {
			return receipt, err
		}
		if response.Term < 1 {
			return receipt, projection.Failure{Code: "response_invalid"}
		}
		if refreshed.Shards == nil || refreshed.Shards.Total < 1 || refreshed.Shards.Failed != 0 {
			return receipt, projection.Failure{Code: "visibility_pending", Retryable: true}
		}
		current.Applied = true
		current.PreviousMode = current.Mode
		if err = c.putState(call, q.AlarmID, current, state.version); hasStatus(err, 409) {
			continue
		} else if err != nil {
			return receipt, err
		}
		return receiptFor(current), nil
	}
	return receipt, projection.Failure{Code: "revision_conflict", Retryable: true}
}

func (c *Client) getState(ctx context.Context, q projection.Request) (storedState, error) {
	var out storedState
	err := c.request(ctx, http.MethodGet, "/"+c.stateIndex+"/_doc/"+url.PathEscape(q.AlarmID), nil, nil, &out)
	if err != nil {
		return out, err
	}
	s := out.Source.State
	if !out.Found || out.Term < 1 || s.Request.Validate() != nil || s.Request.TenantID != q.TenantID || s.Request.AlertID != q.AlertID || s.Request.TargetID != q.TargetID || !c.physicalIndex(s.Index) || s.StorageTime == "" || len(s.Fields) == 0 {
		return out, projection.Failure{Code: "response_invalid"}
	}
	return out, nil
}

func (c *Client) putState(ctx context.Context, id string, s syncState, v version) error {
	var out struct {
		version
		ID     string `json:"_id"`
		Result string `json:"result"`
	}
	err := c.request(ctx, http.MethodPut, "/"+c.stateIndex+"/_doc/"+url.PathEscape(id), v.query(), stateDocument{State: s}, &out)
	if err != nil {
		return err
	}
	if out.Term < 1 || out.ID != id || (out.Result != "updated" && out.Result != "created") {
		return projection.Failure{Code: "response_invalid"}
	}
	return nil
}

func (c *Client) getAlarm(ctx context.Context, index, id string) (alarmDocument, error) {
	var out alarmDocument
	err := c.request(ctx, http.MethodGet, "/"+url.PathEscape(index)+"/_doc/"+url.PathEscape(id), nil, nil, &out)
	if err == nil && (!out.Found || out.Term < 1) {
		return out, projection.Failure{Code: "response_invalid"}
	}
	return out, err
}

func (c *Client) initialize(ctx context.Context, q projection.Request) (storedState, error) {
	if err := c.request(ctx, http.MethodHead, "/"+c.stateIndex, nil, nil, nil); err != nil {
		return storedState{}, err
	}
	fields, mode, err := c.fields(ctx, q)
	if err != nil {
		return storedState{}, err
	}
	// 元数据缺失时也核对已有稳定文档，不能在轮转后把同一 alarm_id 写到新 write index。
	var found struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				Index string `json:"_index"`
				ID    string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}
	body := map[string]any{"size": 2, "_source": false, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"alarm_id": q.AlarmID}}, map[string]any{"term": map[string]any{"bk_tenant_id": q.TenantID}}}}}}
	err = c.request(ctx, http.MethodPost, "/"+url.PathEscape(c.config.AlarmEventIndex)+"/_search", nil, body, &found)
	if err != nil {
		return storedState{}, err
	}
	if found.TimedOut || found.Shards.Failed != 0 || len(found.Hits.Hits) > 1 {
		return storedState{}, projection.Failure{Code: "response_invalid"}
	}
	index := ""
	if len(found.Hits.Hits) == 1 {
		hit := found.Hits.Hits[0]
		if hit.ID != q.AlarmID || !c.physicalIndex(hit.Index) {
			return storedState{}, projection.Failure{Code: "revision_conflict"}
		}
		index = hit.Index
	} else {
		index, err = c.writeIndex(ctx)
		if err != nil {
			return storedState{}, err
		}
	}
	var a domain.Alert
	if json.Unmarshal(q.Alert, &a) != nil {
		return storedState{}, projection.ErrInvalid
	}
	s := syncState{Request: q.Clone(), Index: index, Fields: fields, Mode: mode, StorageTime: time.Now().In(kacZone).Format(kacTimeLayout)}
	err = c.request(ctx, http.MethodPut, "/"+c.stateIndex+"/_create/"+url.PathEscape(q.AlarmID), nil, stateDocument{State: s}, nil)
	if err != nil && !hasStatus(err, 409) {
		return storedState{}, err
	}
	return c.getState(ctx, q)
}

const kacTimeLayout = "2006-01-02 15:04:05"

var kacZone = time.FixedZone("Asia/Shanghai", 8*3600)

func (c *Client) fields(ctx context.Context, q projection.Request) (map[string]any, string, error) {
	var a domain.Alert
	if json.Unmarshal(q.Alert, &a) != nil {
		return nil, "", projection.ErrInvalid
	}
	raw, err := kachook.CompatibilityPayload(a, c.level)
	if err != nil {
		return nil, "", projection.Failure{Code: "response_invalid"}
	}
	fields := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&fields) != nil {
		return nil, "", projection.ErrInvalid
	}
	// KAC Integer 字段不能直接写旧消息里的空字符串；缺值写 null，已有数值保持整数精度。
	for _, key := range []string{"bk_biz_id", "bk_cloud_id", "bk_service_id"} {
		value, _ := fields[key].(string)
		if value == "" {
			fields[key] = nil
			continue
		}
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return nil, "", projection.Failure{Code: "response_invalid"}
		}
		fields[key] = n
	}
	fields["source_alarm_status"] = fields["action"]
	parents := map[string]bool{}
	members := map[string]bool{}
	if a.Merge != nil && len(a.Merge.RelationIDs) > 0 {
		if c.relations == nil {
			return nil, "", projection.Failure{Code: "target_unavailable", Retryable: true}
		}
		for _, id := range a.Merge.RelationIDs {
			relation, e := c.relations.GetMergeRelation(ctx, a.BKTenantID, id)
			if e != nil {
				return nil, "", projection.Failure{Code: "target_unavailable", Retryable: true}
			}
			if relation.TenantID != a.BKTenantID || relation.ID != id {
				return nil, "", projection.ErrInvalid
			}
			if a.Merge.Role == "original" {
				parent, e := projection.AlarmID(a.BKTenantID, relation.ParentAlertID)
				if e != nil {
					return nil, "", e
				}
				parents[parent] = true
			} else {
				if relation.ParentAlertID != a.AlertID {
					return nil, "", projection.ErrInvalid
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
	case a.Shield.Active:
		mode = "shielded"
	case a.Merge != nil && a.Merge.Role == "original" && len(a.Merge.RelationIDs) > 0:
		mode = "merged_into"
	case a.Merge.Blocking():
		mode = "pending_merge"
	}
	return fields, mode, nil
}

func receiptFor(s syncState) projection.Receipt {
	var a struct {
		Status domain.AlertStatus `json:"status"`
	}
	_ = json.Unmarshal(s.Request.Alert, &a)
	q := s.Request
	return projection.Receipt{SchemaVersion: projection.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, AppliedRevision: q.Revision, ContentHash: q.ContentHash, AppliedStatus: a.Status, SearchVisible: true, DocumentRef: s.Index + "/" + q.AlarmID}
}

// isPolicyStatus 也识别已写兼容文档但尚未确认元数据的策略状态。
// 新版本可以合并尚未完成的旧意图，不能仅凭 PreviousMode 判断文档是否已经处于屏蔽/合并状态。
func isPolicyStatus(s string) bool {
	return s == "shielded" || s == "pending_merge" || s == "merged_into"
}

func isDisposalStatus(s string) bool {
	switch s {
	case "abnormal", "dispatched", "pending_execute", "executing", "autoorder_executing", "autoexecute_executing", "autoexecuting_failure":
		return true
	}
	return false
}

func cloneFields(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mergeFields(previous map[string]any, patch any) map[string]any {
	out := cloneFields(previous)
	fields, _ := patch.(map[string]any)
	for k, v := range fields {
		if old, ok := out[k].(map[string]any); ok {
			if _, ok := v.(map[string]any); ok {
				out[k] = mergeFields(old, v)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func mergeTags(previous []any, patch any) []any {
	out := append([]any(nil), previous...)
	tags, _ := patch.([]any)
	for _, tag := range tags {
		exists := false
		want, _ := json.Marshal(tag)
		for _, old := range out {
			got, _ := json.Marshal(old)
			if string(got) == string(want) {
				exists = true
				break
			}
		}
		if !exists {
			out = append(out, tag)
		}
	}
	return out
}

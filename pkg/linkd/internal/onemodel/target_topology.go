// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Members 从当前租户业务的拓扑投影读取节点及完整主机成员，不把 locator 当成实例 ID。
// 本适配器读取 CMDB 主线主机投影；其他模型必须使用其实际成员来源，不能当成主机结果返回。
func (c *Client) Members(ctx context.Context, tenant string, biz int64, model, nodeID string) ([]InstanceRef, bool, error) {
	if ctx == nil || biz < 1 || !selectionText(nodeID, 256) {
		return nil, false, ErrInvalidQuery
	}
	if err := validateOneModelIdentity("tenant", tenant, 64); err != nil {
		return nil, false, err
	}
	if model != hostModelCode {
		return nil, false, fmt.Errorf("%w: topology model requires another member source", ErrTargetUnavailable)
	}
	locator := map[string]any{"bool": map[string]any{"should": []any{term("unique_id", nodeID), term("topology_node_id", nodeID)}, "minimum_should_match": 1}}
	var response targetScrollResponse
	err := c.targetRequest(ctx, http.MethodPost, "/"+c.topologyNodeIndex+"/_search", map[string]any{"size": 2, "track_total_hits": false, "query": map[string]any{"bool": map[string]any{"filter": []any{term("bk_tenant_id", tenant), term("bk_biz_id", biz), locator}}}}, &response)
	if err != nil {
		return nil, false, err
	}
	if response.TimedOut || response.Shards.Failed > 0 || response.Hits.Hits == nil || len(response.Hits.Hits) > 1 {
		return nil, false, ErrInvalidDataSourceResponse
	}
	if len(response.Hits.Hits) == 0 {
		return []InstanceRef{}, false, nil
	}
	nodes := []map[string]any{response.Hits.Hits[0].Source}
	node := nodes[0]
	savedBiz, ok := positiveInt64(node["bk_biz_id"])
	nodeModel, _ := node["model_id"].(string)
	instanceID, _ := node["model_inst_id"].(string)
	locatorID, _ := node["topology_node_id"].(string)
	if locatorID == "" {
		locatorID, _ = node["unique_id"].(string)
	}
	if node["bk_tenant_id"] != tenant || !ok || savedBiz != biz || locatorID != nodeID || !selectionText(nodeModel, 128) || !selectionText(instanceID, 1024) {
		return nil, false, ErrInvalidDataSourceResponse
	}
	if uid, exists := node["entity_uid"]; exists && uid != "" && uid != nodeModel+"|"+instanceID {
		return nil, false, ErrInvalidDataSourceResponse
	}
	rows, err := c.targetMemberships(ctx, tenant, biz, nodeID)
	if err != nil {
		return nil, false, err
	}
	refs := map[string]InstanceRef{}
	for _, row := range rows {
		savedBiz, ok := positiveInt64(row["bk_biz_id"])
		id, _ := row["model_inst_id"].(string)
		ancestors, valid := stringSlice(row["topology_ancestor_unique_ids"])
		if row["bk_tenant_id"] != tenant || !ok || savedBiz != biz || row["model_id"] != model || !selectionText(id, 1024) || row["entity_uid"] != model+"|"+id || !valid || !slices.Contains(ancestors, nodeID) {
			return nil, false, ErrInvalidDataSourceResponse
		}
		refs[id] = InstanceRef{ModelID: model, InstanceID: id, EntityUID: model + "|" + id}
		if len(refs) > 10000 {
			return nil, false, ErrResultLimit
		}
	}
	result := make([]InstanceRef, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref)
	}
	slices.SortFunc(result, func(a, b InstanceRef) int { return strings.Compare(a.InstanceID, b.InstanceID) })
	return result, true, nil
}

type targetScrollResponse struct {
	ScrollID string `json:"_scroll_id"`
	TimedOut bool   `json:"timed_out"`
	Shards   struct {
		Failed int `json:"failed"`
	} `json:"_shards"`
	Hits struct {
		Hits []struct {
			Source map[string]any `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

func (c *Client) targetMemberships(ctx context.Context, tenant string, biz int64, nodeID string) (result []map[string]any, err error) {
	// Scroll 只在本次有界内部查询中使用，读取结束/取消/异常都释放；租户和业务条件固定在首次快照。
	// 不将外部游标拼接到索引路径，也不依赖可变化 offset 分页。
	cursor := ""
	defer func() {
		if cursor != "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			var ignored map[string]any
			err = errors.Join(err, c.targetRequest(cleanup, http.MethodDelete, "/_search/scroll", map[string]any{"scroll_id": []string{cursor}}, &ignored))
		}
	}()
	path := "/" + c.topologyMembershipIndex + "/_search?scroll=1m"
	// ES Scroll 不允许禁用 track_total_hits；完整性仍按逐页身份与数量预算验证，不依赖总数字段。
	body := map[string]any{"size": 200, "sort": []string{"_doc"}, "_source": []string{"unique_id", "bk_tenant_id", "bk_biz_id", "model_id", "model_inst_id", "entity_uid", "topology_ancestor_unique_ids"}, "query": map[string]any{"bool": map[string]any{"filter": []any{term("bk_tenant_id", tenant), term("bk_biz_id", biz), term("model_id", hostModelCode), term("topology_ancestor_unique_ids", nodeID)}}}}
	seen := map[string]bool{}
	result = []map[string]any{}
	totalBytes := 0
	for pages := 0; pages <= 100; pages++ {
		var response targetScrollResponse
		requestErr := c.targetRequest(ctx, http.MethodPost, path, body, &response)
		if response.ScrollID != "" {
			cursor = response.ScrollID
		}
		if requestErr != nil {
			return nil, requestErr
		}
		if cursor == "" || response.TimedOut || response.Shards.Failed > 0 || response.Hits.Hits == nil || len(response.Hits.Hits) > 200 {
			return nil, ErrInvalidDataSourceResponse
		}
		for _, hit := range response.Hits.Hits {
			// 单页响应上限不足以约束整次 Scroll 的内存；祖先路径可让少量成员占用大量字节。
			// 与目标实例分页相同，累计超过 32 MiB 时拒绝整个集合并由 defer 释放快照。
			raw, err := json.Marshal(hit.Source)
			if err != nil {
				return nil, ErrInvalidDataSourceResponse
			}
			totalBytes += len(raw)
			if totalBytes > 32<<20 {
				return nil, ErrResultLimit
			}
			id, _ := hit.Source["unique_id"].(string)
			if id == "" || seen[id] {
				return nil, ErrInvalidDataSourceResponse
			}
			seen[id] = true
			result = append(result, hit.Source)
			if len(result) > 20000 {
				return nil, ErrResultLimit
			}
		}
		if len(response.Hits.Hits) < 200 {
			return result, nil
		}
		path = "/_search/scroll"
		body = map[string]any{"scroll": "1m", "scroll_id": cursor}
	}
	return nil, ErrResultLimit
}

func (c *Client) targetRequest(ctx context.Context, method, path string, body, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.transport.Perform(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxOneModelResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxOneModelResponseBytes {
		return ErrResultLimit
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("onemodel topology request: HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("%w: invalid topology response", ErrInvalidDataSourceResponse)
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return ErrInvalidDataSourceResponse
	}
	return nil
}

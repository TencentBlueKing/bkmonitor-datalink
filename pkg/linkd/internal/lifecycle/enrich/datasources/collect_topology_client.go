// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

const (
	hostModelCode            = "cw-Host"
	bizModelCode             = "cw-biz"
	maxHostMemberships       = 64
	maxHostTopologyAncestors = 256
)

// FindRelatedHost 按统一投影边的正反向关系读取唯一关联主机。
func (c *OneModelClient) FindRelatedHost(
	ctx context.Context,
	tenantID, modelCode, instanceID, hostRelatedField string,
) (enrich.Instance, bool, error) {
	if ctx == nil {
		return enrich.Instance{}, false, fmt.Errorf("find related host: context must not be nil")
	}
	if tenantID == "" || modelCode == "" || instanceID == "" || hostRelatedField == "" {
		return enrich.Instance{}, false, fmt.Errorf("find related host: tenant, model, instance, and relation are required")
	}
	queries := [][]any{
		{
			term("bk_tenant_id", tenantID), term("producer", "cmdb_fact"),
			term("source_model_id", modelCode), term("source_entity_uid", modelCode+"|"+instanceID),
			term("target_model_id", hostModelCode), term("relation_identity", hostRelatedField),
		},
		{
			term("bk_tenant_id", tenantID), term("producer", "cmdb_fact"),
			term("target_model_id", modelCode), term("target_entity_uid", modelCode+"|"+instanceID),
			term("source_model_id", hostModelCode), term("relation_identity", hostRelatedField),
		},
	}
	for _, filters := range queries {
		edge, found, err := c.searchUnique(ctx, oneModelEdgeIndex, filters)
		if err != nil {
			return enrich.Instance{}, false, fmt.Errorf("find related host: query relation: %w", err)
		}
		if !found {
			continue
		}
		hostID := relatedHostID(edge, modelCode)
		if hostID == "" {
			return enrich.Instance{}, false, fmt.Errorf("%w: related host edge identity is invalid", enrich.ErrInvalidDataSourceResponse)
		}
		host, found, err := c.FindInstance(ctx, tenantID, enrich.InstanceQuery{ModelCode: hostModelCode, InstanceID: hostID})
		if err != nil {
			return enrich.Instance{}, false, fmt.Errorf("find related host: query host: %w", err)
		}
		return host, found, nil
	}
	return enrich.Instance{}, false, nil
}

// FindHostTopology 读取主机 membership 和祖先节点，生成业务、集群和模块投影。
// 旧 KAC 回调投影首条路径；ES 命中顺序不稳定，因此按祖先节点 sort_order 与
// 路径 ID 确定稳定的首路径。同一主机可有多个合法模块，但所有 membership 必须先通过身份校验。
func (c *OneModelClient) FindHostTopology(
	ctx context.Context,
	tenantID, hostID string,
) (models.ResourceTopology, bool, error) {
	if ctx == nil {
		return models.ResourceTopology{}, false, fmt.Errorf("find host topology: context must not be nil")
	}
	if tenantID == "" || hostID == "" {
		return models.ResourceTopology{}, false, fmt.Errorf("find host topology: tenant and host ID are required")
	}
	memberships, err := c.searchAll(ctx, c.topologyMembershipIndex, []any{
		term("bk_tenant_id", tenantID), term("model_id", hostModelCode), term("model_inst_id", hostID),
	}, maxHostMemberships+1)
	if err != nil {
		return models.ResourceTopology{}, false, fmt.Errorf("find host topology: query membership: %w", err)
	}
	if len(memberships) == 0 {
		return models.ResourceTopology{}, false, nil
	}
	if len(memberships) > maxHostMemberships {
		return models.ResourceTopology{}, false, fmt.Errorf("%w: host topology membership limit exceeded", enrich.ErrInvalidDataSourceResponse)
	}
	businessID, err := validateHostMemberships(memberships, tenantID, hostID)
	if err != nil {
		return models.ResourceTopology{}, false, err
	}
	// 同业务的多条路径依次按根至叶节点的 sort_order 排序；路径 ID 用于并列顺序兜底。
	// 先批量读取所有候选祖先，避免 membership 增长时产生逐节点往返。
	ancestorSet := make(map[string]struct{})
	for _, membership := range memberships {
		ancestorIDs, _ := stringSlice(membership["topology_ancestor_unique_ids"])
		for _, ancestorID := range ancestorIDs {
			ancestorSet[ancestorID] = struct{}{}
		}
	}
	if len(ancestorSet) > maxHostTopologyAncestors {
		return models.ResourceTopology{}, false, fmt.Errorf("%w: host topology ancestor limit exceeded", enrich.ErrInvalidDataSourceResponse)
	}
	ancestorIDs := make([]string, 0, len(ancestorSet))
	for ancestorID := range ancestorSet {
		ancestorIDs = append(ancestorIDs, ancestorID)
	}
	slices.Sort(ancestorIDs)
	items, err := c.searchAll(ctx, c.topologyNodeIndex, []any{
		term("bk_tenant_id", tenantID), term("bk_biz_id", businessID), terms("unique_id", ancestorIDs),
	}, len(ancestorIDs))
	if err != nil {
		return models.ResourceTopology{}, false, fmt.Errorf("find host topology: query nodes: %w", err)
	}
	if len(items) != len(ancestorIDs) {
		return models.ResourceTopology{}, false, fmt.Errorf("%w: topology ancestor is missing or duplicated", enrich.ErrInvalidDataSourceResponse)
	}
	nodes := make(map[string]map[string]any, len(items))
	for _, node := range items {
		uniqueID, _ := node["unique_id"].(string)
		if node["bk_tenant_id"] != tenantID || uniqueID == "" {
			return models.ResourceTopology{}, false, fmt.Errorf("%w: topology ancestor identity is invalid", enrich.ErrInvalidDataSourceResponse)
		}
		if nodeBizID, ok := positiveInt64(node["bk_biz_id"]); !ok || nodeBizID != businessID {
			return models.ResourceTopology{}, false, fmt.Errorf("%w: topology ancestor business is invalid", enrich.ErrInvalidDataSourceResponse)
		}
		if _, expected := ancestorSet[uniqueID]; !expected {
			return models.ResourceTopology{}, false, fmt.Errorf("%w: topology ancestor identity is invalid", enrich.ErrInvalidDataSourceResponse)
		}
		if _, exists := nodes[uniqueID]; exists {
			return models.ResourceTopology{}, false, fmt.Errorf("%w: topology ancestor identity is duplicated", enrich.ErrInvalidDataSourceResponse)
		}
		nodes[uniqueID] = node
	}
	// 只投影首条路径，保持 BaseTarget 单个 set/module 的输出边界。
	slices.SortFunc(memberships, func(left, right map[string]any) int {
		leftIDs, _ := stringSlice(left["topology_ancestor_unique_ids"])
		rightIDs, _ := stringSlice(right["topology_ancestor_unique_ids"])
		for index := range min(len(leftIDs), len(rightIDs)) {
			leftOrder, _ := nodes[leftIDs[index]]["sort_order"].(string)
			rightOrder, _ := nodes[rightIDs[index]]["sort_order"].(string)
			if comparison := strings.Compare(leftOrder, rightOrder); comparison != 0 {
				return comparison
			}
			if comparison := strings.Compare(leftIDs[index], rightIDs[index]); comparison != 0 {
				return comparison
			}
		}
		return len(leftIDs) - len(rightIDs)
	})
	selectedIDs, _ := stringSlice(memberships[0]["topology_ancestor_unique_ids"])
	result := models.ResourceTopology{BKBizID: businessID}
	for _, ancestorID := range selectedIDs {
		node := nodes[ancestorID]
		modelCode, _ := node["model_id"].(string)
		instanceID, validID := positiveInt64(node["model_inst_id"])
		name, _ := node["bk_inst_name"].(string)
		objectID, _ := node["bk_obj_id"].(string)
		switch {
		case objectID == "biz" && modelCode == bizModelCode:
			if validID && instanceID == businessID {
				result.BKBizName = name
			}
		case modelCode == "cw-Set":
			if validID {
				result.BKSetID, result.BKSetName = instanceID, name
			}
		case modelCode == "cw-Module":
			if validID {
				result.BKModuleID, result.BKModuleName = instanceID, name
			}
		}
	}
	return result, true, nil
}

func validateHostMemberships(memberships []map[string]any, tenantID, hostID string) (int64, error) {
	var businessID int64
	seen := make(map[string]struct{}, len(memberships))
	for _, membership := range memberships {
		if membership["bk_tenant_id"] != tenantID || membership["model_id"] != hostModelCode ||
			membership["model_inst_id"] != hostID || membership["entity_uid"] != hostModelCode+"|"+hostID {
			return 0, fmt.Errorf("%w: host topology membership identity is invalid", enrich.ErrInvalidDataSourceResponse)
		}
		currentBizID, ok := positiveInt64(membership["bk_biz_id"])
		ancestorIDs, okAncestors := stringSlice(membership["topology_ancestor_unique_ids"])
		if !ok || !okAncestors || len(ancestorIDs) == 0 {
			return 0, fmt.Errorf("%w: host topology membership is invalid", enrich.ErrInvalidDataSourceResponse)
		}
		if businessID != 0 && currentBizID != businessID {
			return 0, fmt.Errorf("%w: host topology has multiple business identities", enrich.ErrInvalidDataSourceResponse)
		}
		businessID = currentBizID
		path := strings.Join(ancestorIDs, "\x00")
		if _, exists := seen[path]; exists {
			return 0, fmt.Errorf("%w: duplicate host topology membership", enrich.ErrInvalidDataSourceResponse)
		}
		seen[path] = struct{}{}
	}
	return businessID, nil
}

func (c *OneModelClient) searchUnique(ctx context.Context, index string, filters []any) (map[string]any, bool, error) {
	items, err := c.searchAll(ctx, index, filters, 2)
	if err != nil {
		return nil, false, err
	}
	if len(items) == 0 {
		return nil, false, nil
	}
	if len(items) != 1 {
		return nil, false, fmt.Errorf("%w: query returned multiple identities", enrich.ErrInvalidDataSourceResponse)
	}
	return items[0], true, nil
}

func (c *OneModelClient) searchAll(ctx context.Context, index string, filters []any, size int) ([]map[string]any, error) {
	body, err := json.Marshal(map[string]any{
		"size": size, "track_total_hits": false,
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode query: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/"+index+"/_search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.transport.Perform(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, maxOneModelResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(data) > maxOneModelResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxOneModelResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("elasticsearch status %d", response.StatusCode)
	}
	var result struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: decode response: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	if result.TimedOut || result.Shards.Failed > 0 {
		return nil, fmt.Errorf("%w: incomplete search timed_out=%t failed_shards=%d", enrich.ErrInvalidDataSourceResponse, result.TimedOut, result.Shards.Failed)
	}
	items := make([]map[string]any, len(result.Hits.Hits))
	for index := range result.Hits.Hits {
		items[index] = result.Hits.Hits[index].Source
	}
	return items, nil
}

func terms(field string, values []string) map[string]any {
	return map[string]any{"terms": map[string]any{field: values}}
}

func term(field string, value any) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

func relatedHostID(edge map[string]any, modelCode string) string {
	sourceModel, _ := edge["source_model_id"].(string)
	targetModel, _ := edge["target_model_id"].(string)
	sourceUID, _ := edge["source_entity_uid"].(string)
	targetUID, _ := edge["target_entity_uid"].(string)
	switch {
	case sourceModel == modelCode && targetModel == hostModelCode:
		return strings.TrimPrefix(targetUID, hostModelCode+"|")
	case targetModel == modelCode && sourceModel == hostModelCode:
		return strings.TrimPrefix(sourceUID, hostModelCode+"|")
	default:
		return ""
	}
}

func positiveInt64(value any) (int64, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case string:
		text = typed
	case int64:
		return typed, typed > 0
	case float64:
		text = strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return 0, false
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	return parsed, err == nil && parsed > 0
}

func stringSlice(value any) ([]string, bool) {
	switch items := value.(type) {
	case []any:
		result := make([]string, len(items))
		for index, item := range items {
			text, valid := item.(string)
			if !valid || text == "" {
				return nil, false
			}
			result[index] = text
		}
		return result, true
	case []string:
		for _, item := range items {
			if item == "" {
				return nil, false
			}
		}
		return append([]string(nil), items...), true
	default:
		return nil, false
	}
}

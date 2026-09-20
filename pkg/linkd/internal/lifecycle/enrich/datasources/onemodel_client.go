// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"linkd/internal/lifecycle/enrich"
)

const (
	maxOneModelResponseBytes = 1 << 20
	oneModelInstanceIndex    = "kingeye_all_instance"
	oneModelEdgeIndex        = "kingeye_topo"
)

var (
	_ enrich.OneModelReader        = (*OneModelClient)(nil)
	_ enrich.CollectTopologyReader = (*OneModelClient)(nil)
)

// OneModelClientConfig 注入 OneModel Elasticsearch 的只读传输。
type OneModelClientConfig struct {
	Transport   ElasticsearchTransport
	IndexPrefix string
}

// ElasticsearchTransport 是 OneModelClient 使用的最小 ES 传输端口。
type ElasticsearchTransport interface {
	Perform(request *http.Request) (*http.Response, error)
}

// OneModelClient 按 OneModel 统一实例契约读取 kingeye_all_instance。
type OneModelClient struct {
	transport               ElasticsearchTransport
	topologyNodeIndex       string
	topologyMembershipIndex string
}

// NewOneModelClient 创建统一实例查询 Client；Transport 的生命周期由装配层管理。
func NewOneModelClient(config OneModelClientConfig) (*OneModelClient, error) {
	if config.Transport == nil {
		return nil, fmt.Errorf("create onemodel client: transport must not be nil")
	}
	if config.IndexPrefix == "" {
		config.IndexPrefix = "bk_monitor_base_"
	}
	if err := validateIndexPrefix(config.IndexPrefix); err != nil {
		return nil, fmt.Errorf("create onemodel client: index prefix: %w", err)
	}
	return &OneModelClient{
		transport:               config.Transport,
		topologyNodeIndex:       config.IndexPrefix + "cmdb_biz_topo_node",
		topologyMembershipIndex: config.IndexPrefix + "cmdb_biz_topo_host_membership",
	}, nil
}

// FindInstance 按租户、模型、实例身份及类型化属性精确匹配第一条统一实例。
// 返回前会再次校验文档租户、模型、实例和 entity_uid，避免错误 alias 造成跨边界数据泄漏。
func (c *OneModelClient) FindInstance(
	ctx context.Context,
	tenantID string,
	query enrich.InstanceQuery,
) (enrich.Instance, bool, error) {
	if ctx == nil {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: context must not be nil")
	}
	if tenantID == "" || query.ModelCode == "" {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: tenant ID and model code are required")
	}
	if err := validateOneModelIdentity("model code", query.ModelCode, 128); err != nil {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: %w", err)
	}
	if query.InstanceID != "" {
		if err := validateOneModelIdentity("instance ID", query.InstanceID, 1024); err != nil {
			return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: %w", err)
		}
	}
	if query.InstanceID == "" && len(query.AttributeFilters) == 0 {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: instance ID or attribute filters are required")
	}
	request, err := c.buildFindInstanceRequest(ctx, tenantID, query)
	if err != nil {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: %w", err)
	}
	response, err := c.transport.Perform(request)
	if err != nil {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: search: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	instance, found, err := parseFindInstanceResponse(response, tenantID, query)
	if err != nil {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: %w", err)
	}
	return instance, found, nil
}

func (c *OneModelClient) buildFindInstanceRequest(
	ctx context.Context,
	tenantID string,
	query enrich.InstanceQuery,
) (*http.Request, error) {
	filters := make([]any, 0, len(query.AttributeFilters)+3)
	filters = append(filters,
		map[string]any{"term": map[string]any{"bk_tenant_id": tenantID}},
		map[string]any{"term": map[string]any{"model_id": query.ModelCode}},
	)
	if query.InstanceID != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"model_inst_id": query.InstanceID}})
	}
	for _, filter := range query.AttributeFilters {
		clause, err := oneModelAttributeFilter(filter)
		if err != nil {
			return nil, err
		}
		filters = append(filters, clause)
	}
	body, err := json.Marshal(map[string]any{
		"size":             2,
		"track_total_hits": false,
		"query":            map[string]any{"bool": map[string]any{"filter": filters}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode query: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/"+oneModelInstanceIndex+"/_search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func oneModelAttributeFilter(filter enrich.InstanceAttributeFilter) (map[string]any, error) {
	if err := validateFieldName(filter.Field); err != nil {
		return nil, fmt.Errorf("attribute filter field: %w", err)
	}
	slots := map[enrich.InstanceAttributeType]string{
		enrich.InstanceAttributeKeyword: "keyword_values", enrich.InstanceAttributeLong: "long_values",
		enrich.InstanceAttributeDouble: "double_values", enrich.InstanceAttributeBoolean: "boolean_values",
		enrich.InstanceAttributeDatetime: "datetime_values", enrich.InstanceAttributeIP: "ip_values",
	}
	slot := slots[filter.Type]
	if slot == "" {
		return nil, fmt.Errorf("attribute filter %q has invalid type %q", filter.Field, filter.Type)
	}
	return map[string]any{"nested": map[string]any{
		"path": "attribute_values", "score_mode": "none",
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"attribute_values.field_name": filter.Field}},
			map[string]any{"term": map[string]any{"attribute_values." + slot: filter.Value}},
		}}},
	}}, nil
}

func parseFindInstanceResponse(
	response *http.Response,
	tenantID string,
	query enrich.InstanceQuery,
) (enrich.Instance, bool, error) {
	limited := io.LimitReader(response.Body, maxOneModelResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return enrich.Instance{}, false, fmt.Errorf("read response: %w", err)
	}
	if len(data) > maxOneModelResponseBytes {
		return enrich.Instance{}, false, fmt.Errorf("response exceeds %d bytes", maxOneModelResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return enrich.Instance{}, false, fmt.Errorf("elasticsearch status %d", response.StatusCode)
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
		return enrich.Instance{}, false, fmt.Errorf("%w: decode response: %w", enrich.ErrInvalidDataSourceResponse, err)
	}
	// HTTP 200 的部分失败不等价于实例不存在，必须交给调用方重试。
	if result.TimedOut || result.Shards.Failed > 0 {
		return enrich.Instance{}, false, fmt.Errorf("%w: incomplete search timed_out=%t failed_shards=%d", enrich.ErrInvalidDataSourceResponse, result.TimedOut, result.Shards.Failed)
	}
	if len(result.Hits.Hits) == 0 {
		return enrich.Instance{}, false, nil
	}
	if len(result.Hits.Hits) != 1 {
		return enrich.Instance{}, false, fmt.Errorf("%w: query returned multiple instance identities", enrich.ErrInvalidDataSourceResponse)
	}
	return parseInstanceSource(result.Hits.Hits[0].Source, tenantID, query)
}

func parseInstanceSource(source map[string]any, tenantID string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if source["bk_tenant_id"] != tenantID || source["model_id"] != query.ModelCode {
		return enrich.Instance{}, false, fmt.Errorf("%w: response identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	instanceID, ok := source["model_inst_id"].(string)
	if !ok || instanceID == "" {
		return enrich.Instance{}, false, fmt.Errorf("%w: response has invalid instance identity", enrich.ErrInvalidDataSourceResponse)
	}
	if query.InstanceID != "" && instanceID != query.InstanceID {
		return enrich.Instance{}, false, fmt.Errorf("%w: response instance identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	entityUID, ok := source["entity_uid"].(string)
	if !ok || entityUID != query.ModelCode+"|"+instanceID {
		return enrich.Instance{}, false, fmt.Errorf("%w: response has inconsistent entity identity", enrich.ErrInvalidDataSourceResponse)
	}
	attributes, ok := source["attributes"].(map[string]any)
	if !ok {
		return enrich.Instance{}, false, fmt.Errorf("%w: response attributes must be an object", enrich.ErrInvalidDataSourceResponse)
	}
	return enrich.Instance{
		TenantID: tenantID, ModelCode: query.ModelCode, InstanceID: instanceID,
		Fields: source, Attributes: attributes,
	}, true, nil
}

func validateIndexPrefix(value string) error {
	if value == "" || strings.ContainsAny(value, `/\\?#, *<>|\"`) || strings.HasPrefix(value, "_") || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return fmt.Errorf("invalid index prefix %q", value)
	}
	return nil
}

func validateOneModelIdentity(name, value string, maxBytes int) error {
	if !utf8.ValidString(value) || len([]byte(value)) > maxBytes {
		return fmt.Errorf("%s is invalid", name)
	}
	for _, char := range value {
		if char < ' ' || char == '\u007f' {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	if name == "model code" && strings.ContainsRune(value, '|') {
		return fmt.Errorf("%s contains the entity identity separator", name)
	}
	return nil
}

func validateFieldName(value string) error {
	if value == "" || strings.HasPrefix(value, "_") || strings.ContainsAny(value, ".*?,# ") {
		return fmt.Errorf("invalid field %q", value)
	}
	switch value {
	case "bk_tenant_id", "model_id", "model_inst_id", "entity_uid", "attributes", "attribute_values":
		return fmt.Errorf("reserved field %q", value)
	default:
		return nil
	}
}

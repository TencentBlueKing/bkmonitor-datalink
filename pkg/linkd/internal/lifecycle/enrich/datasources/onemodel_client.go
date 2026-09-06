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
	"net/url"
	"strings"

	"linkd/internal/lifecycle/enrich"
)

const maxOneModelResponseBytes = 1 << 20

var _ enrich.OneModelReader = (*OneModelClient)(nil)

// OneModelClientConfig 注入 OneModel Elasticsearch 的只读传输和 CMDB 实例索引。
type OneModelClientConfig struct {
	Transport ElasticsearchTransport
	CMDBIndex string
}

// ElasticsearchTransport 是 OneModelClient 使用的最小 ES 传输端口。
type ElasticsearchTransport interface {
	Perform(request *http.Request) (*http.Response, error)
}

// OneModelClient 按 OneModel 当前模型路由和扁平字段协议读取实例文档。
type OneModelClient struct {
	transport ElasticsearchTransport
	cmdbIndex string
}

// NewOneModelClient 创建统一实例查询 Client；Transport 的生命周期由装配层管理。
func NewOneModelClient(config OneModelClientConfig) (*OneModelClient, error) {
	if config.Transport == nil {
		return nil, fmt.Errorf("create onemodel client: transport must not be nil")
	}
	if err := validateIndexName(config.CMDBIndex); err != nil {
		return nil, fmt.Errorf("create onemodel client: cmdb index: %w", err)
	}
	return &OneModelClient{transport: config.Transport, cmdbIndex: config.CMDBIndex}, nil
}

// FindInstance 按租户、模型及扁平属性精确匹配第一条实例。
// 返回前会再次校验文档租户和模型，避免错误 alias 或路由造成跨边界数据泄漏。
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
	request, err := c.buildFindInstanceRequest(ctx, tenantID, query)
	if err != nil {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: %w", err)
	}
	response, err := c.transport.Perform(request)
	if err != nil {
		return enrich.Instance{}, false, fmt.Errorf("find onemodel instance: search: %w", err)
	}
	defer response.Body.Close()

	instance, found, err := parseFindInstanceResponse(response, tenantID, query.ModelCode)
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
	index, err := c.indexForModel(query.ModelCode)
	if err != nil {
		return nil, err
	}
	filters := make([]any, 0, len(query.Filters)+2)
	filters = append(filters,
		map[string]any{"term": map[string]any{"bk_tenant_id": tenantID}},
		map[string]any{"term": map[string]any{"cw_object_model_code": query.ModelCode}},
	)
	for field, value := range query.Filters {
		if err := validateFieldName(field); err != nil {
			return nil, fmt.Errorf("filter field: %w", err)
		}
		filters = append(filters, map[string]any{"term": map[string]any{field: value}})
	}
	body, err := json.Marshal(map[string]any{
		"size":             1,
		"track_total_hits": false,
		"query":            map[string]any{"bool": map[string]any{"filter": filters}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode query: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/"+url.PathEscape(index)+"/_search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func parseFindInstanceResponse(
	response *http.Response,
	tenantID string,
	modelCode string,
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
		Hits struct {
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return enrich.Instance{}, false, fmt.Errorf("%w: decode response: %v", enrich.ErrInvalidDataSourceResponse, err)
	}
	if len(result.Hits.Hits) == 0 {
		return enrich.Instance{}, false, nil
	}
	return parseInstanceSource(result.Hits.Hits[0].Source, tenantID, modelCode)
}

func parseInstanceSource(source map[string]any, tenantID, modelCode string) (enrich.Instance, bool, error) {
	if source["bk_tenant_id"] != tenantID || source["cw_object_model_code"] != modelCode {
		return enrich.Instance{}, false, fmt.Errorf("%w: response identity does not match query", enrich.ErrInvalidDataSourceResponse)
	}
	instanceID, ok := source["cw_object_model_inst_id"].(string)
	if !ok || instanceID == "" {
		return enrich.Instance{}, false, fmt.Errorf("%w: response has invalid instance identity", enrich.ErrInvalidDataSourceResponse)
	}
	return enrich.Instance{TenantID: tenantID, ModelCode: modelCode, InstanceID: instanceID, Fields: source}, true, nil
}

func (c *OneModelClient) indexForModel(modelCode string) (string, error) {
	indexes := map[string]string{
		"cw-K8s_Cluster": "kingeye_k8s_cluster", "cw-K8s_Namespace": "kingeye_k8s_namespace",
		"cw-K8s_Node": "kingeye_k8s_node", "cw-K8s_Service": "kingeye_k8s_service",
		"cw-K8s_Workload": "kingeye_k8s_workload", "cw-K8s_Pod": "kingeye_k8s_pod",
		"cw-K8s_Container": "kingeye_k8s_container", "cw-K8s_PersistentVolume": "kingeye_k8s_pv",
		"cw-K8s_PersistentVolumeClaim": "kingeye_k8s_pvc", "cw-application": "kingeye_apm_application",
		"cw-service": "kingeye_apm_service", "cw-service_instance": "kingeye_apm_service_instance",
		"cw-apm_endpoint": "kingeye_apm_endpoint", "cw-apm_component": "kingeye_apm_component_node",
	}
	index := indexes[modelCode]
	if index == "" {
		if strings.HasPrefix(modelCode, "cw-Cloud") || strings.HasPrefix(modelCode, "Cloud") {
			index = "kingeye_cloud_instance"
		} else {
			index = c.cmdbIndex
		}
	}
	return index, validateIndexName(index)
}

func validateIndexName(value string) error {
	if value == "" || strings.ContainsAny(value, `/\\?#, *<>|\"`) || value == "." || value == ".." {
		return fmt.Errorf("invalid index %q", value)
	}
	return nil
}

func validateFieldName(value string) error {
	if value == "" || strings.HasPrefix(value, "_") || strings.ContainsAny(value, "*?,# ") || value == "bk_tenant_id" || value == "cw_object_model_code" {
		return fmt.Errorf("invalid or reserved field %q", value)
	}
	return nil
}

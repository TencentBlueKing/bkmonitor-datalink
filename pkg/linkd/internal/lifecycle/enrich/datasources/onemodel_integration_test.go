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
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"linkd/internal/lifecycle/enrich"
	es "linkd/internal/store/elasticsearch"
)

// TestElasticsearchOneModelContract 用独立索引验证真实 ES 的扁平查询和租户隔离。
func TestElasticsearchOneModelContract(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL to run OneModel contract")
	}
	transport, err := es.NewHTTPTransport(es.HTTPTransportConfig{Addresses: []string{endpoint}, APIKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY"), MaxConnectionsPerHost: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	index := fmt.Sprintf("linkd-onemodel-compat-%d-%d", os.Getpid(), time.Now().UnixNano())
	request := func(method, path, body string) {
		req, err := http.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := transport.Perform(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode >= 300 {
			t.Fatalf("ES %s returned %d", method, response.StatusCode)
		}
		_, _ = io.Copy(io.Discard, response.Body)
	}
	request(http.MethodPut, "/"+index, `{"mappings":{"properties":{"bk_tenant_id":{"type":"keyword"},"cw_object_model_code":{"type":"keyword"},"cw_object_model_inst_id":{"type":"keyword"}}}}`)
	defer request(http.MethodDelete, "/"+index, "")
	request(http.MethodPut, "/"+index+"/_doc/a?refresh=wait_for", `{"bk_tenant_id":"tenant-a","cw_object_model_code":"cw-Host","cw_object_model_inst_id":"101"}`)
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport, CMDBIndex: index})
	if err != nil {
		t.Fatal(err)
	}
	query := enrich.InstanceQuery{ModelCode: "cw-Host", Filters: map[string]any{"cw_object_model_inst_id": "101"}}
	item, found, err := client.FindInstance(t.Context(), "tenant-a", query)
	if err != nil || !found || item.InstanceID != "101" {
		t.Fatalf("found=%t err=%v", found, err)
	}
	_, found, err = client.FindInstance(t.Context(), "tenant-b", query)
	if err != nil || found {
		t.Fatalf("cross-tenant found=%t err=%v", found, err)
	}
}

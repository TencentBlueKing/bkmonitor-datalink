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
	"os"
	"testing"

	es "linkd/internal/store/elasticsearch"
)

// TestElasticsearchOneModelContract 用统一实例 alias 验证真实 ES 的身份查询和租户隔离。
// 测试只读取既有实例；统一实例环境变量缺失时跳过该用例。
func TestElasticsearchOneModelContract(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	tenantID := os.Getenv("LINKD_TEST_ONEMODEL_TENANT_ID")
	modelID := os.Getenv("LINKD_TEST_ONEMODEL_MODEL_ID")
	instanceID := os.Getenv("LINKD_TEST_ONEMODEL_INSTANCE_ID")
	if endpoint == "" || tenantID == "" || modelID == "" || instanceID == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL and LINKD_TEST_ONEMODEL_* to run OneModel contract")
	}
	transport, err := es.NewHTTPTransport(es.HTTPTransportConfig{
		Addresses: []string{endpoint}, APIKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY"), MaxConnectionsPerHost: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	client, err := NewClient(ClientConfig{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	query := InstanceQuery{ModelCode: modelID, InstanceID: instanceID}
	item, found, err := client.FindInstance(t.Context(), tenantID, query)
	if err != nil || !found || item.InstanceID != instanceID {
		t.Fatalf("found=%t item=%#v err=%v", found, item, err)
	}
	missingTenant := tenantID + "-linkd-missing"
	_, found, err = client.FindInstance(t.Context(), missingTenant, query)
	if err != nil || found {
		t.Fatalf("cross-tenant found=%t err=%v", found, err)
	}
}

// TestElasticsearchOneModelPagination 只读验证真实 ES 的 PIT 排序及翻页。
// 不创建或清理业务文档；测试最多读取两个实例，并释放未读完的快照。
func TestElasticsearchOneModelPagination(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	tenant := os.Getenv("LINKD_TEST_ONEMODEL_TENANT_ID")
	model := os.Getenv("LINKD_TEST_ONEMODEL_MODEL_ID")
	if endpoint == "" || tenant == "" || model == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL, LINKD_TEST_ONEMODEL_TENANT_ID and LINKD_TEST_ONEMODEL_MODEL_ID to run PIT integration")
	}
	transport, err := es.NewHTTPTransport(es.HTTPTransportConfig{Addresses: []string{endpoint}, APIKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY"), MaxConnectionsPerHost: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	client, err := NewClient(ClientConfig{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	pager, err := NewPager(client)
	if err != nil {
		t.Fatal(err)
	}
	query := PageQuery{ModelID: model, Limit: 1}
	page, err := pager.Search(t.Context(), tenant, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Instances) != 1 {
		t.Fatal("integration model must contain at least one instance")
	}
	if page.NextCursor == "" {
		t.Log("single-instance model: verified PIT and terminal page only")
		return
	}
	firstID := page.Instances[0].InstanceID
	query.Cursor = page.NextCursor
	page, err = pager.Search(t.Context(), tenant, query)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "" {
		defer func() {
			if err := pager.Close(t.Context(), tenant, page.NextCursor); err != nil {
				t.Error(err)
			}
		}()
	}
	if len(page.Instances) != 1 || page.Instances[0].TenantID != tenant || page.Instances[0].InstanceID <= firstID {
		t.Fatalf("unexpected second page %+v", page)
	}
}

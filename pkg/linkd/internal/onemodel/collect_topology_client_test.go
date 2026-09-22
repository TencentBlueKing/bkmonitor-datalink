// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

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
	"testing"
)

func TestClientFindRelatedHost(t *testing.T) {
	t.Parallel()
	var requests int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		data, _ := io.ReadAll(request.Body)
		body := string(data)
		switch request.URL.Path {
		case "/kingeye_topo/_search":
			for _, expected := range []string{"tenant-a", "cmdb_fact", "cw-Service|2", "cw-Host", "service_run_host"} {
				if !strings.Contains(body, expected) {
					t.Fatalf("edge query=%s missing %q", body, expected)
				}
			}
			return topologySearchResponse(map[string]any{
				"bk_tenant_id": "tenant-a", "producer": "cmdb_fact",
				"source_model_id": "cw-Service", "source_entity_uid": "cw-Service|2",
				"target_model_id": "cw-Host", "target_entity_uid": "cw-Host|101",
				"relation_identity": "service_run_host",
			}), nil
		case "/kingeye_all_instance/_search":
			return topologySearchResponse(map[string]any{
				"bk_tenant_id": "tenant-a", "model_id": "cw-Host", "model_inst_id": "101", "entity_uid": "cw-Host|101",
				"attributes": map[string]any{"bk_host_id": 101},
			}), nil
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
			return nil, nil
		}
	})
	client, _ := NewClient(ClientConfig{Transport: transport})
	host, found, err := client.FindRelatedHost(context.Background(), "tenant-a", "cw-Service", "2", "service_run_host")
	if err != nil || !found || host.InstanceID != "101" || requests != 2 {
		t.Fatalf("FindRelatedHost()=%#v,%t,%v requests=%d", host, found, err, requests)
	}
}

func TestClientFindRelatedHostRejectsAmbiguousEdge(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		document := map[string]any{
			"bk_tenant_id": "tenant-a", "producer": "cmdb_fact",
			"source_model_id": "cw-Service", "source_entity_uid": "cw-Service|2",
			"target_model_id": "cw-Host", "target_entity_uid": "cw-Host|101",
			"relation_identity": "service_run_host",
		}
		return topologySearchResponse(document, document), nil
	})
	client, _ := NewClient(ClientConfig{Transport: transport})
	_, found, err := client.FindRelatedHost(context.Background(), "tenant-a", "cw-Service", "2", "service_run_host")
	if found || err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func TestClientFindHostTopology(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(request.Body)
		body := string(data)
		switch request.URL.Path {
		case "/bk_monitor_base_cmdb_biz_topo_host_membership/_search":
			return topologySearchResponse(map[string]any{
				"bk_tenant_id": "tenant-a", "bk_biz_id": 2,
				"model_id": "cw-Host", "model_inst_id": "101", "entity_uid": "cw-Host|101",
				"topology_ancestor_unique_ids": []string{"biz-2", "set-3", "module-7"},
			}), nil
		case "/bk_monitor_base_cmdb_biz_topo_node/_search":
			documents := make([]map[string]any, 0, 3)
			if strings.Contains(body, "biz-2") {
				documents = append(documents, topologyNode("tenant-a", 2, "biz-2", "cw-biz", "2", "订单业务"))
			}
			if strings.Contains(body, "set-3") {
				documents = append(documents, topologyNode("tenant-a", 2, "set-3", "cw-Set", "3", "生产集群"))
			}
			if strings.Contains(body, "module-7") {
				documents = append(documents, topologyNode("tenant-a", 2, "module-7", "cw-Module", "7", "订单模块"))
			}
			if len(documents) != 3 {
				t.Fatalf("unexpected query %s", body)
			}
			return topologySearchResponse(documents...), nil
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
			return nil, nil
		}
	})
	client, _ := NewClient(ClientConfig{Transport: transport})
	topology, found, err := client.FindHostTopology(context.Background(), "tenant-a", "101")
	if err != nil || !found || topology.BKBizID != 2 || topology.BKBizName != "订单业务" ||
		topology.BKSetID != 3 || topology.BKSetName != "生产集群" ||
		topology.BKModuleID != 7 || topology.BKModuleName != "订单模块" {
		t.Fatalf("FindHostTopology()=%#v,%t,%v", topology, found, err)
	}
}

func TestClientFindHostTopologySelectsStablePath(t *testing.T) {
	t.Parallel()
	memberships := []map[string]any{
		{"bk_tenant_id": "tenant-a", "bk_biz_id": 2, "model_id": "cw-Host", "model_inst_id": "101",
			"entity_uid": "cw-Host|101", "topology_unique_id": "module-8", "topology_ancestor_unique_ids": []string{"biz-2", "set-4", "module-8"}},
		{"bk_tenant_id": "tenant-a", "bk_biz_id": 2, "model_id": "cw-Host", "model_inst_id": "101",
			"entity_uid": "cw-Host|101", "topology_unique_id": "module-7", "topology_ancestor_unique_ids": []string{"biz-2", "set-3", "module-7"}},
	}
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint("reverse=", reverse), func(t *testing.T) {
			ordered := append([]map[string]any(nil), memberships...)
			if reverse {
				slices.Reverse(ordered)
			}
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(request.Body)
				switch request.URL.Path {
				case "/bk_monitor_base_cmdb_biz_topo_host_membership/_search":
					return topologySearchResponse(ordered...), nil
				case "/bk_monitor_base_cmdb_biz_topo_node/_search":
					documents := make([]map[string]any, 0, 5)
					if bytes.Contains(body, []byte("biz-2")) {
						documents = append(documents, topologyNode("tenant-a", 2, "biz-2", "cw-biz", "2", "订单业务"))
					}
					if bytes.Contains(body, []byte("set-3")) {
						documents = append(documents, topologyNodeWithOrder("tenant-a", 2, "set-3", "cw-Set", "3", "生产集群", "0000000003"))
					}
					if bytes.Contains(body, []byte("module-7")) {
						documents = append(documents, topologyNodeWithOrder("tenant-a", 2, "module-7", "cw-Module", "7", "订单模块", "0000000007"))
					}
					if bytes.Contains(body, []byte("set-4")) {
						documents = append(documents, topologyNodeWithOrder("tenant-a", 2, "set-4", "cw-Set", "4", "备用集群", "0000000004"))
					}
					if bytes.Contains(body, []byte("module-8")) {
						documents = append(documents, topologyNodeWithOrder("tenant-a", 2, "module-8", "cw-Module", "8", "备用模块", "0000000008"))
					}
					if len(documents) != 5 {
						t.Fatalf("unexpected ancestor query %s", body)
					}
					return topologySearchResponse(documents...), nil
				default:
					t.Fatalf("unexpected path %s", request.URL.Path)
					return nil, nil
				}
			})
			client, _ := NewClient(ClientConfig{Transport: transport})
			topology, found, err := client.FindHostTopology(context.Background(), "tenant-a", "101")
			if err != nil || !found || topology.BKBizName != "订单业务" || topology.BKSetID != 3 || topology.BKModuleID != 7 {
				t.Fatalf("FindHostTopology()=%#v,%t,%v", topology, found, err)
			}
		})
	}
}

func TestClientFindHostTopologyRejectsInvalidNodes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		nodes []map[string]any
	}{
		{name: "missing", nodes: []map[string]any{}},
		{name: "duplicate", nodes: []map[string]any{
			topologyNode("tenant-a", 2, "biz-2", "cw-biz", "2", "业务"),
			topologyNode("tenant-a", 2, "biz-2", "cw-biz", "2", "业务"),
		}},
		{name: "foreign tenant", nodes: []map[string]any{topologyNode("tenant-b", 2, "biz-2", "cw-biz", "2", "业务")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Path {
				case "/bk_monitor_base_cmdb_biz_topo_host_membership/_search":
					return topologySearchResponse(map[string]any{
						"bk_tenant_id": "tenant-a", "bk_biz_id": 2, "model_id": "cw-Host", "model_inst_id": "101",
						"entity_uid": "cw-Host|101", "topology_ancestor_unique_ids": []string{"biz-2"},
					}), nil
				case "/bk_monitor_base_cmdb_biz_topo_node/_search":
					return topologySearchResponse(test.nodes...), nil
				default:
					t.Fatalf("unexpected path %s", request.URL.Path)
					return nil, nil
				}
			})
			client, _ := NewClient(ClientConfig{Transport: transport})
			_, found, err := client.FindHostTopology(context.Background(), "tenant-a", "101")
			if found || !errors.Is(err, ErrInvalidDataSourceResponse) {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}

func TestClientFindHostTopologyRejectsInvalidMembership(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		memberships []map[string]any
	}{
		{name: "wrong tenant", memberships: []map[string]any{{"bk_tenant_id": "tenant-b", "bk_biz_id": 2,
			"model_id": "cw-Host", "model_inst_id": "101", "entity_uid": "cw-Host|101",
			"topology_ancestor_unique_ids": []string{"biz-2", "module-7"}}}},
		{name: "missing ancestor", memberships: []map[string]any{{"bk_tenant_id": "tenant-a", "bk_biz_id": 2,
			"model_id": "cw-Host", "model_inst_id": "101", "entity_uid": "cw-Host|101",
			"topology_ancestor_unique_ids": []string{}}}},
		{name: "cross business", memberships: []map[string]any{
			{"bk_tenant_id": "tenant-a", "bk_biz_id": 2, "model_id": "cw-Host", "model_inst_id": "101",
				"entity_uid": "cw-Host|101", "topology_ancestor_unique_ids": []string{"biz-2", "module-7"}},
			{"bk_tenant_id": "tenant-a", "bk_biz_id": 3, "model_id": "cw-Host", "model_inst_id": "101",
				"entity_uid": "cw-Host|101", "topology_ancestor_unique_ids": []string{"biz-3", "module-8"}},
		}},
		{name: "duplicate path", memberships: []map[string]any{
			{"bk_tenant_id": "tenant-a", "bk_biz_id": 2, "model_id": "cw-Host", "model_inst_id": "101",
				"entity_uid": "cw-Host|101", "topology_ancestor_unique_ids": []string{"biz-2", "module-7"}},
			{"bk_tenant_id": "tenant-a", "bk_biz_id": 2, "model_id": "cw-Host", "model_inst_id": "101",
				"entity_uid": "cw-Host|101", "topology_ancestor_unique_ids": []string{"biz-2", "module-7"}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/bk_monitor_base_cmdb_biz_topo_host_membership/_search" {
					t.Fatalf("unexpected path %s", request.URL.Path)
				}
				return topologySearchResponse(test.memberships...), nil
			})
			client, _ := NewClient(ClientConfig{Transport: transport})
			_, found, err := client.FindHostTopology(context.Background(), "tenant-a", "101")
			if found || !errors.Is(err, ErrInvalidDataSourceResponse) {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}

func topologyNodeWithOrder(tenant string, bizID int64, uniqueID, modelID, instanceID, name, sortOrder string) map[string]any {
	node := topologyNode(tenant, bizID, uniqueID, modelID, instanceID, name)
	node["sort_order"] = sortOrder
	return node
}

func topologyNode(tenant string, bizID int64, uniqueID, modelID, instanceID, name string) map[string]any {
	objectID := ""
	if modelID == "cw-biz" {
		objectID = "biz"
	}
	return map[string]any{
		"bk_tenant_id": tenant, "bk_biz_id": bizID, "unique_id": uniqueID,
		"model_id": modelID, "model_inst_id": instanceID, "entity_uid": modelID + "|" + instanceID,
		"bk_obj_id": objectID, "bk_inst_name": name,
	}
}

func topologySearchResponse(sources ...map[string]any) *http.Response {
	hits := make([]map[string]any, len(sources))
	for index, source := range sources {
		hits[index] = map[string]any{"_source": source}
	}
	data, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data))}
}

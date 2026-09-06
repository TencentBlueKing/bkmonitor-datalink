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
	"io"
	"net/http"
	"strings"
	"testing"

	"linkd/internal/lifecycle/enrich"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) Perform(request *http.Request) (*http.Response, error) { return fn(request) }

func TestOneModelClientFindInstance(t *testing.T) {
	t.Parallel()
	var gotPath string
	var gotBody string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath = request.URL.Path
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = string(data)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"hits":{"hits":[{"_source":{"bk_tenant_id":"tenant-a","cw_object_model_code":"cw-Host","cw_object_model_inst_id":"101","bk_host_id":101}}]}}`)),
		}, nil
	})
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport, CMDBIndex: "bk_monitor_base_cmdb_instance"})
	if err != nil {
		t.Fatal(err)
	}
	instance, found, err := client.FindInstance(context.Background(), "tenant-a", enrich.InstanceQuery{
		ModelCode: "cw-Host", Filters: map[string]any{"cw_object_model_inst_id": float64(101)},
	})
	if err != nil || !found || instance.InstanceID != "101" {
		t.Fatalf("FindInstance()=%#v,%v,%v", instance, found, err)
	}
	if gotPath != "/bk_monitor_base_cmdb_instance/_search" {
		t.Fatalf("path=%q", gotPath)
	}
	for _, value := range []string{"tenant-a", "cw-Host", "cw_object_model_inst_id"} {
		if !bytes.Contains([]byte(gotBody), []byte(value)) {
			t.Fatalf("body=%s missing %q", gotBody, value)
		}
	}
}

func TestOneModelClientRoutesK8sAndRejectsCrossTenantResponse(t *testing.T) {
	t.Parallel()
	var gotPath string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath = request.URL.Path
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"hits":{"hits":[{"_source":{"bk_tenant_id":"tenant-b","cw_object_model_code":"cw-K8s_Cluster","cw_object_model_inst_id":"c1"}}]}}`)),
		}, nil
	})
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport, CMDBIndex: "cmdb_instance"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.FindInstance(context.Background(), "tenant-a", enrich.InstanceQuery{ModelCode: "cw-K8s_Cluster"})
	if err == nil || !strings.Contains(err.Error(), "identity does not match") {
		t.Fatalf("FindInstance() error=%v", err)
	}
	if gotPath != "/kingeye_k8s_cluster/_search" {
		t.Fatalf("path=%q", gotPath)
	}
}

func TestOneModelClientReturnsMissing(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hits":{"hits":[]}}`))}, nil
	})
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport, CMDBIndex: "cmdb_instance"})
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.FindInstance(context.Background(), "tenant-a", enrich.InstanceQuery{ModelCode: "cw-Host"})
	if err != nil || found {
		t.Fatalf("FindInstance() found=%v error=%v", found, err)
	}
}

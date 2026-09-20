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

func TestOneModelClientFindInstanceByIdentity(t *testing.T) {
	t.Parallel()
	var gotPath string
	var gotBody []byte
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath = request.URL.Path
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = data
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"hits":{"hits":[{"_source":{
				"bk_tenant_id":"system","model_id":"cw-Host","model_inst_id":"167","entity_uid":"cw-Host|167",
				"source":"cmdb","display_name":"10.10.28.10","bk_biz_ids":[2],
				"attributes":{"bk_host_id":167,"bk_host_name":"linux-28-10","bk_biz_id":2,"bk_biz_name":"蓝鲸-修改后2"},
				"attribute_values":[{"field_name":"bk_host_id","long_values":[167]}]
			}}]}}`)),
		}, nil
	})
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	instance, found, err := client.FindInstance(context.Background(), "system", enrich.InstanceQuery{
		ModelCode: rulesHostModel, InstanceID: "167",
	})
	if err != nil || !found || instance.InstanceID != "167" || instance.Attributes["bk_host_name"] != "linux-28-10" {
		t.Fatalf("FindInstance()=%#v,%v,%v", instance, found, err)
	}
	if gotPath != "/kingeye_all_instance/_search" {
		t.Fatalf("path=%q", gotPath)
	}
	for _, value := range []string{"system", "cw-Host", "model_id", "model_inst_id", "167"} {
		if !bytes.Contains(gotBody, []byte(value)) {
			t.Fatalf("body=%s missing %q", gotBody, value)
		}
	}
	for _, legacy := range []string{"cw_object_model_code", "cw_object_model_inst_id"} {
		if bytes.Contains(gotBody, []byte(legacy)) {
			t.Fatalf("body=%s contains legacy field %q", gotBody, legacy)
		}
	}
}

func TestOneModelClientRejectsUnboundedAndInvalidAttributeQueries(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid query reached transport")
		return nil, nil
	})
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	queries := []enrich.InstanceQuery{
		{ModelCode: rulesHostModel},
		{ModelCode: rulesHostModel, AttributeFilters: []enrich.InstanceAttributeFilter{{Field: "attributes.bk_host_id", Type: enrich.InstanceAttributeLong, Value: 167}}},
		{ModelCode: rulesHostModel, AttributeFilters: []enrich.InstanceAttributeFilter{{Field: "bk_host_id", Type: "unknown", Value: 167}}},
	}
	for _, query := range queries {
		if _, _, err := client.FindInstance(context.Background(), "system", query); err == nil {
			t.Fatalf("query=%#v was accepted", query)
		}
	}
}

func TestOneModelClientBuildsNestedAttributeFilters(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotBody, _ = io.ReadAll(request.Body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hits":{"hits":[]}}`))}, nil
	})
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.FindInstance(context.Background(), "system", enrich.InstanceQuery{
		ModelCode: rulesHostModel,
		AttributeFilters: []enrich.InstanceAttributeFilter{
			{Field: "bk_host_innerip", Type: enrich.InstanceAttributeKeyword, Value: "10.10.28.10"},
			{Field: "bk_cloud_id", Type: enrich.InstanceAttributeLong, Value: float64(0)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"nested", "attribute_values", "bk_host_innerip", "keyword_values", "bk_cloud_id", "long_values"} {
		if !bytes.Contains(gotBody, []byte(value)) {
			t.Fatalf("body=%s missing %q", gotBody, value)
		}
	}
}

func TestOneModelClientRejectsInvalidResponseIdentity(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"bk_tenant_id":"tenant-b","model_id":"cw-Host","model_inst_id":"167","entity_uid":"cw-Host|167","attributes":{}}`,
		`{"bk_tenant_id":"tenant-a","model_id":"cw-Disk","model_inst_id":"167","entity_uid":"cw-Disk|167","attributes":{}}`,
		`{"bk_tenant_id":"tenant-a","model_id":"cw-Host","model_inst_id":"168","entity_uid":"cw-Host|168","attributes":{}}`,
		`{"bk_tenant_id":"tenant-a","model_id":"cw-Host","model_inst_id":"167","entity_uid":"cw-Disk|167","attributes":{}}`,
	}
	for _, source := range cases {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hits":{"hits":[{"_source":` + source + `}]}}`))}, nil
		})
		client, err := NewOneModelClient(OneModelClientConfig{Transport: transport})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = client.FindInstance(context.Background(), "tenant-a", enrich.InstanceQuery{ModelCode: rulesHostModel, InstanceID: "167"})
		if err == nil || !strings.Contains(err.Error(), "identity") {
			t.Fatalf("source=%s error=%v", source, err)
		}
	}
}

func TestOneModelClientRejectsAmbiguousInstance(t *testing.T) {
	t.Parallel()
	document := `{"bk_tenant_id":"tenant-a","model_id":"cw-Host","model_inst_id":"167","entity_uid":"cw-Host|167","attributes":{}}`
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"hits":{"hits":[{"_source":` + document + `},{"_source":` + document + `}]}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	client, _ := NewOneModelClient(OneModelClientConfig{Transport: transport})
	_, found, err := client.FindInstance(context.Background(), "tenant-a", enrich.InstanceQuery{ModelCode: rulesHostModel, InstanceID: "167"})
	if found || err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func TestOneModelClientReturnsMissing(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hits":{"hits":[]}}`))}, nil
	})
	client, err := NewOneModelClient(OneModelClientConfig{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.FindInstance(context.Background(), "tenant-a", enrich.InstanceQuery{ModelCode: rulesHostModel, InstanceID: "missing"})
	if err != nil || found {
		t.Fatalf("FindInstance() found=%v error=%v", found, err)
	}
}

func TestOneModelRejectsPartialSearch(t *testing.T) {
	for _, body := range []string{`{"timed_out":true,"hits":{"hits":[]}}`, `{"_shards":{"failed":1},"hits":{"hits":[]}}`} {
		response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
		_, found, err := parseFindInstanceResponse(response, "tenant-a", enrich.InstanceQuery{ModelCode: rulesHostModel, InstanceID: "167"})
		_ = response.Body.Close()
		if found || err == nil {
			t.Fatalf("found=%t error=%v", found, err)
		}
	}
}

const rulesHostModel = "cw-Host"

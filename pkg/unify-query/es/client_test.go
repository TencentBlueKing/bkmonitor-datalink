// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestSearchAllowsMissingTargets(t *testing.T) {
	log.InitTestLogger()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/missing,existing/_search", r.URL.Path)
		assert.Equal(t, "true", r.URL.Query().Get("ignore_unavailable"))
		assert.Equal(t, "true", r.URL.Query().Get("allow_no_indices"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_, _ = io.WriteString(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
	}))
	defer server.Close()

	client, err := NewClient(&ESInfo{Host: server.URL, MaxConcurrency: 1})
	require.NoError(t, err)
	result, err := client.Search(context.Background(), `{}`, "missing", "existing")
	require.NoError(t, err)
	assert.JSONEq(t, `{"hits":{"total":{"value":0},"hits":[]}}`, result)
}

func TestSearchReturnsElasticsearchError(t *testing.T) {
	log.InitTestLogger()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad query"}`)
	}))
	defer server.Close()

	client, err := NewClient(&ESInfo{Host: server.URL, MaxConcurrency: 1})
	require.NoError(t, err)
	_, err = client.Search(context.Background(), `{}`, "existing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400 Bad Request")
}

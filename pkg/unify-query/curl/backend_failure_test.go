// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package curl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// A backend that answers with a failure status, or cannot be reached, is
// told apart by the status the request carries, never by its words; the
// words stay as they were.
func TestABackendFailureCarriesItsStatus(t *testing.T) {
	for status, want := range map[int]int{
		http.StatusServiceUnavailable:  http.StatusBadGateway,
		http.StatusInternalServerError: http.StatusBadGateway,
		http.StatusUnauthorized:        http.StatusBadGateway,
		http.StatusForbidden:           http.StatusBadGateway,
		http.StatusBadRequest:          http.StatusBadRequest,
		http.StatusNotFound:            http.StatusBadRequest,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			var res map[string]any
			_, err := (&HttpCurl{}).Request(context.Background(), Post, Options{UrlPath: server.URL, Body: []byte(`{}`)}, &res)
			require.Error(t, err)
			require.Equal(t, want, metadata.FailureStatus(err))
			require.Contains(t, err.Error(), fmt.Sprintf("http code error: %d %s in %s", status, http.StatusText(status), server.URL))
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed := server.URL
	server.Close()
	var res map[string]any
	_, err := (&HttpCurl{}).Request(context.Background(), Post, Options{UrlPath: closed, Body: []byte(`{}`)}, &res)
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, metadata.FailureStatus(err), "a closed port: %s", err)
}

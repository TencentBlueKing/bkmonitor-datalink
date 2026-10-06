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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHttpCurlMaxResponseBytes(t *testing.T) {
	body := []byte(`{"value":"0123456789"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	t.Run("accepts response at limit", func(t *testing.T) {
		var response map[string]any
		size, err := (&HttpCurl{}).Request(context.Background(), Get, Options{
			UrlPath:          server.URL,
			MaxResponseBytes: int64(len(body)),
		}, &response)

		require.NoError(t, err)
		assert.Equal(t, len(body), size)
		assert.Equal(t, "0123456789", response["value"])
	})

	t.Run("rejects response above limit before decoding", func(t *testing.T) {
		var response map[string]any
		limit := int64(len(body) - 1)
		size, err := (&HttpCurl{}).Request(context.Background(), Get, Options{
			UrlPath:          server.URL,
			MaxResponseBytes: limit,
		}, &response)

		require.ErrorContains(t, err, "response body exceeds maximum size")
		var limitErr *ResponseBodyLimitError
		require.ErrorAs(t, err, &limitErr)
		assert.Equal(t, "max_response_bytes", limitErr.TruncationReason())
		assert.Equal(t, int(limit+1), size)
		assert.Nil(t, response)
	})
}

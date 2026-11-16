// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadBulkResponse(t *testing.T) {
	// HTTP 200 只表示批量请求已处理，必须继续检查每条 index 操作的结果。
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		itemCount int
		retryable bool
		wantError string
	}{
		{"created", 200, `{"errors":false,"items":[{"index":{"status":201}}]}`, 1, false, ""},
		{"replaced", 200, `{"errors":false,"items":[{"index":{"status":200}}]}`, 1, false, ""},
		{"mixed_items", 200, `{"errors":true,"items":[{"index":{"status":201}},{"index":{"status":429,"error":{"type":"es_rejected_execution_exception"}}}]}`, 2, false, ""},
		{"bad_request", 400, `{"error":"bad request"}`, 1, false, "HTTP 400"},
		{"unauthorized", 401, `{"error":"authentication required"}`, 1, false, "HTTP 401"},
		{"forbidden", 403, `{"error":"forbidden"}`, 1, false, "HTTP 403"},
		{"throttled", 429, `{"error":"busy"}`, 1, true, "HTTP 429"},
		{"server_error", 500, `{"error":"internal error"}`, 1, true, "HTTP 500"},
		{"bad_gateway", 502, "bad gateway", 1, true, "HTTP 502"},
		{"unavailable", 503, `{"error":"unavailable"}`, 1, true, "HTTP 503"},
		{"gateway_timeout", 504, "gateway timeout", 1, true, "HTTP 504"},
		{"truncated_json", 200, `{"items":[`, 1, true, "decode"},
		{"missing_items", 200, `{"errors":false}`, 1, true, "acknowledged 0 of 1"},
		{"extra_items", 200, `{"items":[{"index":{"status":201}},{"index":{"status":201}}]}`, 1, true, "acknowledged 2 of 1"},
		{"missing_status", 200, `{"items":[{"index":{}}]}`, 1, true, "invalid status 0"},
		{"wrong_operation", 200, `{"items":[{"create":{"status":201}}]}`, 1, true, "invalid status 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}
			result, retryable, err := readBulkResponse(response, tc.itemCount)
			require.Equal(t, tc.retryable, retryable)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, result)
				return
			}
			require.NoError(t, err)
			require.Len(t, result.Items, tc.itemCount)
		})
	}
}

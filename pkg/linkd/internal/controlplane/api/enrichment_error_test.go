// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/enrich/custom"
)

func TestEnrichmentCompileErrorIsReturnedWithoutDependencyDetails(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(`{"rules":[{"id":"extract_1","operations":[{"id":"step_1","type":"extract","source":{"literal":"text"},"pattern":"(","assignments":[{"target":"$.title","value":{"literal":"x"}}]}]}]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	_, err := custom.Compile("fields", cfg)
	if err == nil {
		t.Fatal("invalid pattern compiled")
	}
	response := httptest.NewRecorder()
	failure(response, fmt.Errorf("source enrich: %w", err))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "extract_1") {
		t.Fatalf("compile error lost: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	failure(response, fmt.Errorf("private dependency address/password"))
	if strings.Contains(response.Body.String(), "private") {
		t.Fatal("dependency error exposed")
	}
}

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
	"net/http"
	"testing"

	"linkd/internal/config"
	"linkd/internal/policy"
)

func TestShieldBatchPreviewHTTPBoundary(t *testing.T) {
	a := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}, PolicyPreviewer: policy.NewPreviewer(nil, nil, nil, nil, nil)}
	h := a.Handler()
	for _, body := range []string{`{}`, `{"bk_tenant_id":"tenant","type":"shield","alert_ids":[]}`, `{"bk_tenant_id":"tenant","type":"shield","extra":true}`} {
		w := policyHTTP(t, h, http.MethodPost, "/api/v1/policies/preview-batch", body)
		if w.Code != 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/projection"
)

// kacESPlugin 使用真实 ES 保存兼容文档；代理仅注入故障并观察已成功持久化的版本。
// KAC 处置接收器仍是协议模拟，不能据此声明真实 KAC 应用联调完成。
func kacESPlugin(t *testing.T, receiver *kacDeliveryReceiver, endpoint string) config.PluginsConfig {
	t.Helper()
	environment := loadEnvironment(t)
	alias := newResourceNames().IndexPrefix + "-alarm_event"
	target, err := url.Parse(environment.ElasticsearchURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data []byte
		if r.Body != nil {
			var readErr error
			data, readErr = io.ReadAll(io.LimitReader(r.Body, 2<<20))
			if readErr != nil {
				w.WriteHeader(502)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/"+alias+"-") && (strings.Contains(r.URL.Path, "/_create/") || strings.Contains(r.URL.Path, "/_update/")) {
			receiver.projectionAttempts.Add(1)
			if !receiver.allowProjection.Load() {
				w.WriteHeader(503)
				return
			}
		}
		destination := *target
		destination.Path = r.URL.Path
		destination.RawQuery = r.URL.RawQuery
		req, e := http.NewRequestWithContext(r.Context(), r.Method, destination.String(), bytes.NewReader(data))
		if e != nil {
			w.WriteHeader(502)
			return
		}
		req.Header = r.Header.Clone()
		//nolint:gosec // G704: 测试代理只转发到开发者显式配置的隔离 ES，URL host 不来自请求。
		res, e := client.Do(req)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode >= 200 && res.StatusCode < 300 && r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/_doc/") {
			var envelope struct {
				State struct {
					Applied bool               `json:"applied"`
					Index   string             `json:"index"`
					Request projection.Request `json:"request"`
				} `json:"state"`
			}
			if json.Unmarshal(data, &envelope) == nil && envelope.State.Applied {
				q := envelope.State.Request
				var a domain.Alert
				if q.Validate() != nil || json.Unmarshal(q.Alert, &a) != nil {
					t.Error("invalid persisted compatibility intent")
				} else {
					receiver.mu.Lock()
					receiver.projections[q.AlertID] = projection.Receipt{SchemaVersion: projection.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, AppliedRevision: q.Revision, ContentHash: q.ContentHash, AppliedStatus: a.Status, SearchVisible: true, DocumentRef: envelope.State.Index + "/" + q.AlarmID}
					if receiver.projectionAlerts == nil {
						receiver.projectionAlerts = map[string]domain.Alert{}
					}
					receiver.projectionAlerts[q.AlertID] = a
					receiver.mu.Unlock()
				}
			}
		}
		for key, values := range res.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		digest := sha256.Sum256([]byte(alias))
		state := fmt.Sprintf(".linkd-kac-state-%x", digest[:16])
		for _, path := range []string{"/" + alias + "-000001", "/" + alias + "-000002", "/" + state, "/_template/" + alias, "/_ilm/policy/" + alias + "_policy"} {
			uri := *target
			uri.Path = path
			req, e := http.NewRequestWithContext(ctx, http.MethodDelete, uri.String(), nil)
			if e != nil {
				t.Error(e)
				continue
			}
			res, e := client.Do(req)
			if e != nil {
				t.Error(e)
				continue
			}
			_ = res.Body.Close()
			if res.StatusCode != 200 && res.StatusCode != 404 {
				t.Errorf("cleanup isolated KAC resource: %d", res.StatusCode)
			}
		}
	})
	zero := 0
	//nolint:gosec // G101: 仅用于本测试临时处置接收端的合成凭据。
	return config.PluginsConfig{KAC: &config.KACPluginConfig{Enabled: true, AlarmEventIndex: alias, Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{server.URL}, NumberOfShards: 1, NumberOfReplicas: &zero}, ActionEndpoint: endpoint + "/original/action", InternalToken: "e2e-delivery-secret"}}
}

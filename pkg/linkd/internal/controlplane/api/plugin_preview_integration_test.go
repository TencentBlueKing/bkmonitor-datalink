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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"linkd/internal/collector"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/ingestpreview"
	"linkd/internal/pluginruntime"
	"linkd/internal/runtimeconfig"
)

type previewDraft struct{ record eventsource.Record }

func (s previewDraft) Get(context.Context, string) (eventsource.Record, error) { return s.record, nil }

// TestPluginPreviewPythonCleanerIntegration 经管理路由连接真实常驻 Python 执行池和 Go Cleaner。
// 仅配置草稿读取与计算依赖；没有消息发布、接收存储或游标写入端口。
func TestPluginPreviewPythonCleanerIntegration(t *testing.T) {
	python := os.Getenv("LINKD_RUNTIME_PYTHON")
	if python == "" {
		t.Skip("set LINKD_RUNTIME_PYTHON")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	root, err := filepath.Abs("../../../python")
	if err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(t.TempDir(), "token")
	if err = os.WriteFile(token, []byte("preview-test"), 0600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // 显式集成测试解释器与固定参数，凭据来自隔离临时目录。
	command := exec.CommandContext(ctx, python, "-S", "-m", "linkd_runtime", "serve", "--port", "0", "--processes", "2", "--threads", "2", "--token-file", token)
	command.Env = append(os.Environ(), "PYTHONPATH="+root)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Python shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Error("Python shutdown timeout")
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("Python startup failed")
	}
	var address struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err = json.Unmarshal(scanner.Bytes(), &address); err != nil {
		t.Fatal(err)
	}
	executor, err := pluginruntime.NewHTTPClient(&http.Client{Timeout: 5 * time.Second}, fmt.Sprintf("http://127.0.0.1:%d/execute", address.Port), "preview-test", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	source := previewDraft{eventsource.Record{Revision: 1, Spec: config.EventSource{EventSourceID: "source", RelatedTenantID: "tenant", Collection: &config.SourceCollection{Plugin: "rest_api_push", Timezone: "UTC"}}}}
	inspector, err := collector.NewInspector(source, executor, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 2, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	severity := runtimeconfig.NewSeverity(config.DefaultSeverityConfig())
	handler := (&API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}, SourceInspector: inspector, IngestPreviewer: ingestpreview.New(nil, severity.SeveritySnapshot)}).Handler()
	const path = "/api/v1/event-sources/source/plugin-preview"
	fields := policyHTTP(t, handler, "POST", path, `{"request_id":"fields","mode":"fields"}`)
	if fields.Code != 200 {
		t.Fatalf("fields: %d %s", fields.Code, fields.Body.String())
	}
	result := policyHTTP(t, handler, "POST", path, `{"request_id":"sample","mode":"push","received_at":"2026-11-15T00:00:00Z","data":{"bk_tenant_id":"spoof","fingerprint":"original-alert","title":"CPU","evaluations":[{"action":"triggered","severity":"fatal"},{"action":"resolved","severity":"warning"}]}}`)
	if result.Code != 200 {
		t.Fatalf("plugin: %d %s", result.Code, result.Body.String())
	}
	var computed pluginruntime.Result
	if err = json.Unmarshal(result.Body.Bytes(), &computed); err != nil {
		t.Fatal(err)
	}
	if computed.TenantID != "tenant" || len(computed.Events) != 1 {
		t.Fatalf("plugin result: %+v", computed)
	}
	request := ingestpreview.Request{BKTenantID: "tenant", EventSourceID: "source", SessionID: "sample", KACSource: ingestpreview.KACSource{ID: "original", Name: "测试来源"}, Source: ingestpreview.Source{Kind: "draft", Calculation: &ingestpreview.Calculation{Cleaner: config.CleanerConfig{Type: "standard"}, FingerprintMode: "field", FingerprintField: "source_alert_id", SeverityMapping: map[string]string{"fatal": "critical"}}}, Output: "standard", Samples: []ingestpreview.Sample{{ID: "one", ReceivedAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), Payload: computed.Events[0]}}}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	normalized := policyHTTP(t, handler, "POST", "/api/v1/ingest/preview", string(body))
	if normalized.Code != 200 {
		t.Fatalf("cleaner: %d %s", normalized.Code, normalized.Body.String())
	}
	var output ingestpreview.Response
	if err = json.Unmarshal(normalized.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Items) != 2 {
		t.Fatalf("lost evaluations: %+v", output)
	}
	for _, item := range output.Items {
		if !item.Passed || item.StandardEvent["bk_tenant_id"] != "tenant" || item.StandardEvent["fingerprint"] != "original-alert" {
			t.Fatalf("invalid standard result: %+v", item)
		}
	}
	if output.Items[0].Severity != "critical" || output.Items[1].Severity != "warning" {
		t.Fatalf("severity mapping lost: %+v", output.Items)
	}
}

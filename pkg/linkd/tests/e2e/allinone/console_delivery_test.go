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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"linkd/internal/projection"
)

func buildDeliveryConsole(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "pnpm", "build")
	command.Dir = filepath.Join(root, "console")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Console: %v\n%s", err, out)
	}
}

func deliveryTestAddress(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

// 单独启动临时 Prometheus，只抓取本次合成实例；不修改既有服务的配置、规则或数据。
// Apple Container 的宿主可达 IP 必须显式提供，不能假设容器 localhost 就是宿主。
func startDeliveryPrometheus(t *testing.T, backend string) (string, string, string) {
	t.Helper()
	scrapeHost := os.Getenv("LINKD_E2E_PROMETHEUS_SCRAPE_HOST")
	if net.ParseIP(scrapeHost) == nil {
		t.Fatal("set LINKD_E2E_PROMETHEUS_SCRAPE_HOST to the container-reachable host IP")
	}
	if _, err := exec.LookPath("container"); err != nil {
		t.Fatal("Apple Container CLI is required for isolated Prometheus")
	}
	metricsAddress := deliveryTestAddress(t)
	_, metricsPort, err := net.SplitHostPort(metricsAddress)
	if err != nil {
		t.Fatal(err)
	}
	prometheusAddress := deliveryTestAddress(t)
	name := fmt.Sprintf("linkd-e2e-prom-%d-%d", os.Getpid(), time.Now().UnixNano())
	instance := "delivery-" + backend
	document := map[string]any{
		"global":         map[string]any{"scrape_interval": "1s", "scrape_timeout": "1s", "evaluation_interval": "1s"},
		"scrape_configs": []any{map[string]any{"job_name": "linkd-delivery-e2e", "static_configs": []any{map[string]any{"targets": []string{net.JoinHostPort(scrapeHost, metricsPort)}, "labels": map[string]string{"instance": instance}}}}},
	}
	raw, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "prometheus.yml")
	//nolint:gosec // G306: 配置仅含合成实例和抓取地址，需供容器 nobody 用户只读挂载。
	if err := os.WriteFile(configPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	image := os.Getenv("LINKD_E2E_PROMETHEUS_IMAGE")
	if image == "" {
		image = "docker.io/prom/prometheus:latest"
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		//nolint:gosec // G204: 仅删除本测试生成且已启动的唯一名称，不接受外部容器 ID。
		if out, err := exec.CommandContext(ctx, "container", "delete", "--force", name).CombinedOutput(); err != nil {
			t.Errorf("remove owned Prometheus: %v %s", err, out)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	//nolint:gosec // G204: 参数由测试独立配置/端口/名称构成，镜像覆盖是显式测试环境设置。
	command := exec.CommandContext(ctx, "container", "run", "--detach", "--name", name, "--cpus", "1", "--memory", "512m", "--publish", prometheusAddress+":9090", "--volume", configPath+":/etc/prometheus/prometheus.yml:ro", image, "--config.file=/etc/prometheus/prometheus.yml", "--storage.tsdb.path=/tmp/prometheus", "--storage.tsdb.retention.time=1h", "--storage.tsdb.retention.size=32MB", "--log.level=warn")
	out, err := command.CombinedOutput()
	cancel()
	if err != nil {
		// CLI 超时或返回失败时也可能已创建容器，只尝试清理本次唯一名称。
		cleanup()
		t.Fatalf("start isolated Prometheus: %v %s", err, out)
	}
	t.Cleanup(cleanup)
	url := "http://" + prometheusAddress
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+"/-/ready", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		ready := false
		if err == nil {
			ready = response.StatusCode == http.StatusOK
			_ = response.Body.Close()
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated Prometheus did not become ready")
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+"/api/v1/status/buildinfo", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var build struct {
		Status string `json:"status"`
		Data   struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&build)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || build.Status != "success" || build.Data.Version == "" {
		t.Fatal("Prometheus build information unavailable", err)
	}
	t.Logf("owned Prometheus=%s; version=%s; metrics instance=%s", name, build.Data.Version, instance)
	return net.JoinHostPort("0.0.0.0", metricsPort), url, instance
}

func (h *policyHarness) verifyConsoleDelivery(root, backend, prometheusURL, instance, alertID, actionID string) {
	h.t.Helper()
	console, address := h.startConsole(root, "LINKD_CONSOLE_PROMETHEUS_URL="+prometheusURL)
	id, err := projection.TaskID("delivery", alertID, "kac", 2)
	if err != nil {
		h.t.Fatal(err)
	}
	fixture := map[string]string{"backend": backend, "base_url": address + "/real-console", "instance": instance, "alert_id": alertID, "action_task": actionID, "projection_task": id}
	raw, err := json.Marshal(fixture)
	if err != nil {
		h.t.Fatal(err)
	}
	manifest := filepath.Join(h.t.TempDir(), "delivery.json")
	if err := os.WriteFile(manifest, raw, 0600); err != nil {
		h.t.Fatal(err)
	}
	output := filepath.Join(root, "console", "test-results", "delivery-"+backend+"-"+h.names.Token)
	ctx, cancel := context.WithTimeout(h.ctx, 4*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "pnpm", "exec", "playwright", "test", "--config", "playwright.delivery.config.ts")
	command.Dir = filepath.Join(root, "console")
	command.Env = consoleTestEnv("LINKD_CONSOLE_DELIVERY_FIXTURE="+manifest, "LINKD_CONSOLE_REAL_OUTPUT="+output)
	out, err := command.CombinedOutput()
	h.t.Logf("real delivery Console %s: %s", backend, out)
	if err != nil {
		h.t.Fatalf("delivery browser: %v; artifacts=%s", err, output)
	}
	h.t.Logf("delivery browser artifacts: %s", output)
	if err := console.stop(); err != nil {
		h.t.Fatal(err)
	}
}

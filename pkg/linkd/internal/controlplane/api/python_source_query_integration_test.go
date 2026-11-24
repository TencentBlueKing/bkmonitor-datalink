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
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/internaltoken"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

// TestPythonQueriesSourceAlerts 真实 Python 访问正式路由、认证、分页和领域序列化；仓储使用内存实现。
func TestPythonQueriesSourceAlerts(t *testing.T) {
	python := os.Getenv("LINKD_RUNTIME_PYTHON")
	if python == "" {
		t.Skip("set LINKD_RUNTIME_PYTHON")
	}
	repository := memory.New()
	base := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 38; i++ {
		tenant, source := "tenant", "source"
		if i == 36 {
			tenant = "other"
		}
		if i == 37 {
			source = "other"
		}
		id := fmt.Sprintf("alert-%02d", i)
		alert := storetest.Alert(tenant, id, "event-"+id, fmt.Sprintf("fingerprint-%02d", i), "warning")
		alert.EventSourceID = source
		alert.SourceEventID = "upstream-event"
		alert.ExtraData = domain.JSONObject{"meta_info": json.RawMessage(fmt.Sprintf(`"upstream-%02d"`, i))}
		alert.BeginAt = base.Add(time.Duration(i) * time.Minute)
		alert.LastOccurredAt, alert.UpdateAt, alert.CreateAt = alert.BeginAt, alert.BeginAt, alert.BeginAt
		if i == 35 {
			alert.BeginAt = base.Add(24 * time.Hour)
			alert.LastOccurredAt, alert.UpdateAt = alert.BeginAt, alert.BeginAt
		}
		created, err := repository.CreateAlert(t.Context(), alert)
		if err != nil {
			t.Fatal(err)
		}
		if i == 35 {
			terminal := created.Alert.Clone()
			terminal.UpdateAt = terminal.UpdateAt.Add(time.Second)
			end := terminal.UpdateAt
			terminal.Status, terminal.EndAt, terminal.EndType, terminal.EndReason = domain.AlertStatusRecovered, &end, domain.AlertEndTypeSource, "resolved"
			if _, err := repository.CompareAndSetAlert(t.Context(), tenant, id, created.Version, terminal); err != nil {
				t.Fatal(err)
			}
		}
	}
	//nolint:gosec // G101: 仅用于本地隔离测试的固定签名值，不是部署凭据。
	handler := (&API{SourceAlerts: NewSourceAlerts(repository), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "query-signing-key"}}}).Handler()
	server := httptest.NewServer(handler)
	defer server.Close()
	signer, err := internaltoken.New("query-signing-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign("plugin-reader")
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../../python")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	//nolint:gosec // 显式测试解释器和固定脚本，签发凭据仅写临时文件。
	command := exec.CommandContext(ctx, python, "-S", "-c", `import sys
from linkd_runtime.source_alerts import ActiveAlerts
reader = ActiveAlerts(sys.argv[1], token_file=sys.argv[2], timezone="UTC")
active = reader("tenant", "source", {})
assert len(active) == 35, len(active)
assert all(item["bk_tenant_id"] == "tenant" and item["source_id"] == "source" for item in active)
selected = reader("tenant", "source", {"event_id": ["fingerprint-34"]})
assert len(selected) == 1, selected
assert selected[0]["meta_info"] == "upstream-34"
assert selected[0]["level"] == "warning"
assert selected[0]["alarm_time"] == "2026-11-14 12:34:00"
assert selected[0]["dimensions"]["host"] == "host-1"
latest = reader.latest("tenant", "source")
assert latest["fingerprint"] == "fingerprint-35"
assert latest["action"] == "resolved"
assert latest["alarm_time"] == "2026-11-15 12:00:00"
from linkd_runtime.base import BaseClientV2
from linkd_runtime.executor import Executor, Plugin
class Recover(BaseClientV2):
    def pull_recovery_events(self):
        self.match_alarm_resolved(self.query_alarm_source_not_close_alarm_by_event_id(["fingerprint-34"]))
        return self._terminal_events
result = Executor({"demo": Plugin(Recover, ("recovery",))}, active_alerts=reader).execute({"version": 1, "request_id": "recovery-1", "bk_tenant_id": "tenant", "source_id": "source", "plugin": "demo", "mode": "recovery", "received_at": "2026-11-15T13:00:00Z", "timezone": "UTC"})
assert len(result["events"]) == 1
recovered = result["events"][0]
assert recovered["fingerprint"] == "fingerprint-34"
assert recovered["evaluations"] == [{"action": "resolved", "severity": "warning"}]
assert recovered["occurred_at"] == "2026-11-15T13:00:00Z"
`, server.URL, tokenFile)
	command.Env = append(os.Environ(), "PYTHONPATH="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Python source query: %v %s", err, output)
	}
}

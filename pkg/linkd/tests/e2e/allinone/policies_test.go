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
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"go.yaml.in/yaml/v3"
	"linkd/internal/config"
	controlapi "linkd/internal/controlplane/api"
	"linkd/internal/domain"
	"linkd/internal/enrich/view"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/scheduler"
	mergeflow "linkd/internal/merge"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/redisclient"
	"linkd/internal/shieldcheck"
	"linkd/internal/suppressioncheck"
	"linkd/internal/suppressioncleanup"
	"linkd/internal/taskdispatch"
)

// 真正启动 all-in-one 并从 Kafka 输入；后台裁决、解除和 Hook 均由进程自动执行。
// MySQL/OneModel 使用隔离的真实数据，代理只重写固定索引名，不模拟查询结果。
func TestAllInOneEnabledPoliciesE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.checkClip()
			h.checkAggregation()
			h.checkTimeShield()
			h.checkMerge()
			h.checkMergeRecoveryAndFailure()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

// TestAllInOneSuppressionReconcileE2E 单独验证计数/主登记的受控检查，缩短抑制边界回归反馈。
func TestAllInOneSuppressionReconcileE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.checkClip()
			h.checkAggregation()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

type policyHarness struct {
	t             *testing.T
	ctx           context.Context
	names         resourceNames
	process       *linkdProcess
	es            *elasticsearchClient
	db            *sql.DB
	metadata      *sql.DB
	configPath    string
	resources     config.ResourcesConfig
	client        taskdispatch.Client
	kafka         *kgo.Client
	broker        string
	wantActions   map[string][]string
	wantPolicies  map[string]map[string]bool
	failRelations atomic.Bool
}

func startPolicyHarness(t *testing.T, root, binary, backend string) *policyHarness {
	return startPolicyHarnessWithTimeout(t, root, binary, backend, 8*time.Minute)
}

func startPolicyHarnessWithTimeout(t *testing.T, root, binary, backend string, timeout time.Duration, configure ...func(string)) *policyHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	environment, names := loadEnvironment(t), newResourceNames()
	h := &policyHarness{t: t, ctx: ctx, names: names, es: newElasticsearchClient(t, environment.ElasticsearchURL), broker: environment.KafkaBroker, wantActions: map[string][]string{}, wantPolicies: map[string]map[string]bool{}}
	t.Logf("isolated resources: %s; ES=%s", names.IndexPrefix, h.es.version(ctx, t))
	t.Cleanup(func() { cleanupPolicyElasticsearch(t, h.es, names) })
	metadataName := names.MySQLDatabase + "_meta"
	admin, metadata, version := createMySQLDatabase(ctx, t, environment, metadataName)
	h.metadata = metadata
	t.Cleanup(func() { cleanupMySQLDatabase(t, admin, metadata, metadataName) })
	t.Logf("metadata MySQL=%s", version)
	seedPolicyMetadata(t, ctx, metadata)
	if backend == "mysql" {
		a, db, _ := createMySQLDatabase(ctx, t, environment, names.MySQLDatabase)
		h.db = db
		t.Cleanup(func() { cleanupMySQLDatabase(t, a, db, names.MySQLDatabase) })
	}
	proxy := policyOneModelProxy(t, ctx, h.es, names, &h.failRelations)
	rc := redis.NewClient(&redis.Options{Addr: environment.RedisAddress, Password: environment.RedisPassword, DB: environment.RedisDatabase})
	if err := rc.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupPolicyRedis(t, rc, names); _ = rc.Close() })
	h.kafka = newKafkaClient(t, environment.KafkaBroker, "policy-e2e-"+names.Token)
	topics := []string{names.RawTopic, names.RawTopic + "-b", names.OutputTopic, names.OutputTopic + "-actions"}
	createTopics(ctx, t, h.kafka, topics...)
	t.Cleanup(func() { cleanupKafkaTopics(t, h.kafka, topics...); h.kafka.Close() })
	dir := t.TempDir()
	path := writeConfig(t, root, dir, "config."+backend+".template.yaml", environment, names, "policy-a", map[string]string{
		"{{MYSQL_ADDRESS}}": environment.MySQLAddress, "{{MYSQL_DATABASE}}": names.MySQLDatabase,
		"{{MYSQL_USERNAME}}": environment.MySQLUsername, "{{MYSQL_PASSWORD}}": environment.MySQLPassword,
	})
	configurePolicyHarness(t, path, environment, metadataName, proxy.URL, names)
	for _, change := range configure {
		change(path)
	}
	cfg, err := config.Load(path, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	h.configPath = path
	h.resources = cfg.Resources.Clone()
	h.client = taskdispatch.Client{URL: cfg.Dispatch.URL, JWTSecretKey: cfg.Dispatch.JWT.SecretKey, JWTUsername: cfg.Dispatch.JWT.Username}
	h.process = startAllInOne(t, root, binary, path, dir)
	t.Cleanup(func() {
		_ = h.process.stop()
		if t.Failed() {
			data, err := os.ReadFile(h.process.logPath)
			if err == nil {
				// 保留尾部诊断；本测试只有合成 payload 和临时认证信息。
				if len(data) > 48000 {
					data = data[len(data)-48000:]
				}
				t.Logf("all-in-one log tail:\n%s", data)
			}
		}
	})
	importSources(ctx, t, h.process, path)
	ready := names
	ready.SignalStream = mailbox.SourceStream(names.SignalStream, names.Token, "policy-a")
	if h.db == nil {
		waitUntilReady(ctx, t, h.process, h.es, rc, ready)
	} else {
		waitUntilMySQLReady(ctx, t, h.process, h.db, rc, ready)
	}
	for _, source := range []string{"policy-a", "policy-b"} {
		waitTaskCounts(ctx, t, h.client, source, 1, 1)
	}
	var builtin eventsource.Record
	h.call(http.MethodGet, "/api/v1/event-sources/builtin_alarm_merge", nil, &builtin)
	builtin.Spec.Hooks = cfg.EventSources[0].Hooks
	h.call(http.MethodPut, "/api/v1/event-sources/builtin_alarm_merge", controlapi.Mutation{Expected: builtin.Revision, Spec: builtin.Spec}, &builtin)
	h.until("builtin source hook release running", func() bool {
		var state taskdispatch.State
		h.call(http.MethodGet, "/api/v1/runtime", nil, &state)
		for _, task := range state.Tasks {
			if task.Source == "builtin_alarm_merge" && task.Role == "lifecycle" && task.Version == builtin.Revision && task.Phase == "running" {
				return true
			}
		}
		return false
	})
	return h
}

func configurePolicyHarness(t *testing.T, path string, env e2eEnvironment, metadata, endpoint string, names resourceNames) {
	t.Helper()
	//nolint:gosec // G304: path 只来自本测试 writeConfig 创建的临时文件。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["resources"] = map[string]any{
		"mysql":    map[string]any{"address": env.MySQLAddress, "database": metadata, "username": env.MySQLUsername, "password": env.MySQLPassword},
		"onemodel": map[string]any{"addresses": []string{endpoint}, "index_prefix": names.IndexPrefix + "-om-"},
	}
	document["lifecycle"].(map[string]any)["severity_upgrade_policy"] = "update_current"
	source := document["event_sources"].([]any)[0].(map[string]any)
	hooks := source["hooks"].([]any)
	source["hooks"] = append(hooks, map[string]any{"name": "action-kac", "type": "kac", "config": map[string]any{"brokers": []string{env.KafkaBroker}, "topic": names.OutputTopic + "-actions", "security": map[string]any{"protocol": "plaintext"}}})
	var enrich map[string]any
	if err := json.Unmarshal([]byte(`{"processors":[{"type":"fields","config":{"rules":[{"id":"marker","operations":[{"id":"marker","type":"assign","assignments":[{"target":"$.labels.enrich_marker","value":{"literal":"policy-e2e"}}]}]}]}}]}`), &enrich); err != nil {
		t.Fatal(err)
	}
	source["enrich"] = enrich
	var second map[string]any
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &second); err != nil {
		t.Fatal(err)
	}
	second["event_source_id"] = "policy-b"
	input := second["storage"].(map[string]any)["kafka"].(map[string]any)
	input["topic"], input["consumer_group"] = names.RawTopic+"-b", names.CleanerGroup+"-b"
	document["event_sources"] = []any{source, second}
	data, err = yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedPolicyMetadata(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		"CREATE TABLE metadata_space (bk_tenant_id VARCHAR(64), space_type_id VARCHAR(32), space_id VARCHAR(64), is_global BOOLEAN)",
		"CREATE TABLE object_model_v2 (bk_tenant_id VARCHAR(64), model_id VARCHAR(128), datasource VARCHAR(32), bk_cmdb_obj_id VARCHAR(64), attribute_config JSON)",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, tenant := range []string{"clip", "other", "aggregation", "shield", "merge", "dependency-custom", "dependency-cmdb", "merge-cycle", "merge-multi", "merge-combined", "merge-capacity"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO metadata_space VALUES (?, 'bkcc', '2', false)", tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO object_model_v2 VALUES (?, 'cmdb.host', 'cmdb', 'host', '{"config":[]}')`, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO object_model_v2 VALUES (?, 'cmdb.switch', 'cmdb', 'switch', '{"config":[]}')`, tenant); err != nil {
			t.Fatal(err)
		}
	}
}

func policyOneModelProxy(t *testing.T, ctx context.Context, es *elasticsearchClient, names resourceNames, failRelations *atomic.Bool) *httptest.Server {
	t.Helper()
	index := names.IndexPrefix + "-one-instances"
	mapping := []byte(`{"mappings":{"properties":{"bk_tenant_id":{"type":"keyword"},"model_id":{"type":"keyword"},"model_inst_id":{"type":"keyword"},"entity_uid":{"type":"keyword"},"bk_biz_ids":{"type":"long"},"attribute_values":{"type":"nested","properties":{"field_name":{"type":"keyword"},"keyword_values":{"type":"keyword"},"long_values":{"type":"long"}}}}}}`)
	if _, err := es.do(ctx, http.MethodPut, "/"+index, mapping, nil); err != nil {
		t.Fatal(err)
	}
	instance := []byte(`{"bk_tenant_id":"shield","model_id":"cmdb.host","model_inst_id":"host-1","entity_uid":"cmdb.host|host-1","bk_biz_ids":[2],"attributes":{}}`)
	if _, err := es.do(ctx, http.MethodPut, "/"+index+"/_doc/shield-host", instance, nil); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"dependency-custom", "dependency-cmdb"} {
		for _, ref := range [][2]string{{"cmdb.host", "host-1"}, {"cmdb.host", "host-2"}, {"cmdb.switch", "switch-1"}} {
			data, err := json.Marshal(map[string]any{"bk_tenant_id": tenant, "model_id": ref[0], "model_inst_id": ref[1], "entity_uid": ref[0] + "|" + ref[1], "bk_biz_ids": []int{2}, "attributes": map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := es.do(ctx, http.MethodPut, "/"+index+"/_doc/"+tenant+"-"+ref[1], data, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	edges := names.IndexPrefix + "-one-edges"
	edgeMapping := []byte(`{"mappings":{"properties":{"bk_tenant_id":{"type":"keyword"},"producer":{"type":"keyword"},"source_entity_uid":{"type":"keyword"},"target_entity_uid":{"type":"keyword"},"source_model_id":{"type":"keyword"},"target_model_id":{"type":"keyword"},"relation_identity":{"type":"keyword"}}}}`)
	if _, err := es.do(ctx, http.MethodPut, "/"+edges, edgeMapping, nil); err != nil {
		t.Fatal(err)
	}
	for i, ref := range [][4]string{{"cmdb.switch", "switch-1", "cmdb.host", "host-1"}, {"cmdb.host", "host-2", "cmdb.switch", "switch-1"}} {
		data, err := json.Marshal(map[string]any{"bk_tenant_id": "dependency-cmdb", "producer": "cmdb_fact", "source_model_id": ref[0], "source_entity_uid": ref[0] + "|" + ref[1], "target_model_id": ref[2], "target_entity_uid": ref[2] + "|" + ref[3], "relation_identity": "switch_connect_host"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := es.do(ctx, http.MethodPut, fmt.Sprintf("/%s/_doc/edge-%d", edges, i), data, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := es.do(ctx, http.MethodPost, "/"+edges+"/_refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := es.do(ctx, http.MethodPost, "/"+index+"/_refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(es.baseURL)
		r.Out.URL.Path = strings.Replace(r.In.URL.Path, "/kingeye_all_instance", "/"+index, 1)
		r.Out.URL.Path = strings.Replace(r.Out.URL.Path, "/kingeye_topo", "/"+edges, 1)
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只允许该用例所需的 PIT/search 路径，不允许落到共享固定别名。
		ownedTopology := r.URL.Path == "/"+names.IndexPrefix+"-om-cmdb_biz_topo_node/_search" || r.URL.Path == "/"+names.IndexPrefix+"-om-cmdb_biz_topo_host_membership/_search"
		if !ownedTopology && r.URL.Path != "/_search/scroll" && r.URL.Path != "/kingeye_all_instance/_pit" && r.URL.Path != "/_search" && r.URL.Path != "/_pit" && r.URL.Path != "/kingeye_all_instance/_search" && r.URL.Path != "/kingeye_topo/_search" {
			http.Error(w, "unexpected OneModel path", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/kingeye_topo/_search" && failRelations.Load() {
			http.Error(w, "synthetic relation outage", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func (h *policyHarness) call(method, path string, in, out any) {
	h.t.Helper()
	if err := h.client.Call(h.ctx, method, path, in, out); err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
}

func (h *policyHarness) until(description string, ready func() bool) {
	h.t.Helper()
	h.untilWithin(description, 75*time.Second, ready)
}

func (h *policyHarness) untilWithin(description string, timeout time.Duration, ready func() bool) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, timeout)
	defer cancel()
	for {
		if ready() {
			return
		}
		if exited, err := h.process.checkExited(); exited {
			h.t.Fatalf("%s: process exited: %v", description, err)
		}
		select {
		case <-ctx.Done():
			h.t.Fatalf("%s: %v", description, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (h *policyHarness) events() []storedEventView {
	if h.db != nil {
		return loadMySQLEvents(h.ctx, h.t, h.db)
	}
	return loadEvents(h.ctx, h.t, h.es, h.names.EventIndex)
}

func (h *policyHarness) alerts() []domain.Alert {
	if h.db != nil {
		return loadMySQLAlerts(h.ctx, h.t, h.db)
	}
	return loadAlerts(h.ctx, h.t, h.es, h.names.AlertIndex)
}

func (h *policyHarness) alert(id string, test func(domain.Alert) bool) domain.Alert {
	h.t.Helper()
	var found domain.Alert
	h.until("Alert "+id, func() bool {
		for _, alert := range h.alerts() {
			if alert.AlertID == id {
				found = alert
				return test(alert)
			}
		}
		return false
	})
	return found
}

func condition(title string) map[string]any {
	return map[string]any{"expression": "A", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": title}}
}

func (h *policyHarness) publish(tenant string, kind policy.Kind, spec map[string]any, end time.Time) {
	h.t.Helper()
	h.publishID(tenant, kind, "enabled-e2e", spec, end)
}

func (h *policyHarness) publishID(tenant string, kind policy.Kind, id string, spec map[string]any, end time.Time) {
	h.t.Helper()
	spec["name"], spec["is_enable"], spec["updated_at"] = "E2E "+tenant, true, time.Now().UTC()
	if _, exists := spec["space_code"]; !exists {
		spec["space_code"] = "bkcc__2"
	}
	spec["timezone"] = "UTC"
	spec["activate_times"] = []any{map[string]any{"period": "once", "open_datetime_once": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "close_datetime_once": end.UTC().Format(time.RFC3339)}}
	raw, err := json.Marshal(spec)
	if err != nil {
		h.t.Fatal(err)
	}
	request := policy.ApplyRequest{Scope: policy.Scope{TenantID: tenant, Kind: kind}, SchemaVersion: 1, ID: id, OperationID: "publish-" + id, Spec: raw}
	var release policy.Release
	h.call(http.MethodPut, "/api/v1/policies/"+string(kind)+"/"+id, request, &release)
	if h.wantPolicies[tenant] == nil {
		h.wantPolicies[tenant] = map[string]bool{}
	}
	h.wantPolicies[tenant][string(kind)+"/"+id] = true
}

func (h *policyHarness) rawRecord(tenant, source, id, fingerprint, title, object, severity, action string, instance ...map[string]any) *kgo.Record {
	h.t.Helper()
	now := time.Now().UTC()
	labels := map[string]any{"model_id": "cmdb.host", "model_inst_id": "host-1"}
	if len(instance) > 1 {
		h.t.Fatal("one instance override allowed")
	}
	if len(instance) == 1 {
		labels = instance[0]
	}
	payload, err := json.Marshal(map[string]any{
		"bk_tenant_id": tenant, "event_id": id, "alert_id": fingerprint, "title": title, "content": "content-" + id,
		"evaluations": []any{map[string]any{"severity": severity, "action": action}}, "dimensions": map[string]any{"bk_biz_id": 2},
		"subject":     map[string]any{"system": "cmdb", "type": "host", "id": object, "name": object},
		"occurred_at": now, "produced_at": now, "labels": labels,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	topic := h.names.RawTopic
	if source == "policy-b" {
		topic += "-b"
	}
	key := scheduler.CorrelationKey(tenant, source, fingerprint)
	record := &kgo.Record{Topic: topic, Key: []byte(key), Value: payload, Timestamp: now, Headers: []kgo.RecordHeader{
		{Key: "message_id", Value: []byte(id)}, {Key: "bk_tenant_id", Value: []byte(tenant)}, {Key: "order_key", Value: []byte(key)},
	}}
	return record
}

func (h *policyHarness) send(tenant, source, id, fingerprint, title, object, severity, action string, instance ...map[string]any) storedEventView {
	h.t.Helper()
	record := h.rawRecord(tenant, source, id, fingerprint, title, object, severity, action, instance...)
	return h.sendRecord(tenant, id, severity, record)
}

func (h *policyHarness) sendRecord(tenant, id, severity string, record *kgo.Record) storedEventView {
	h.t.Helper()
	if err := h.kafka.ProduceSync(h.ctx, record).FirstErr(); err != nil {
		h.t.Fatal(err)
	}
	var found storedEventView
	h.until("processed Event "+id, func() bool {
		for _, event := range h.events() {
			if event.Event.BKTenantID == tenant && event.Event.SourceEventID == id {
				found = event
				return event.Processing.State != domain.EventProcessStateUnprocessed
			}
		}
		return false
	})
	if found.Event.EnrichStatus != domain.EnrichStatusSucceeded || found.Processing.PolicyContext == nil {
		h.t.Fatalf("Event enrich/policy context missing: %+v", found)
	}
	if len(found.Processing.PolicyContext.Releases) != len(h.wantPolicies[tenant]) || found.Processing.PolicyContext.ReasonCode != "" {
		h.t.Fatalf("enabled policy release was not frozen: %+v", found.Processing.PolicyContext)
	}
	for _, ref := range found.Processing.PolicyContext.Releases {
		if !h.wantPolicies[tenant][ref.Kind+"/"+ref.ID] {
			h.t.Fatal("unexpected policy in frozen context")
		}
	}
	effective, err := view.EnrichedEvent(found.Event, severity)
	marker, ok := effective.Labels["enrich_marker"].StringValue()
	if err != nil || !ok || marker != "policy-e2e" {
		h.t.Fatalf("enriched Event marker: %+v %v", effective.Labels, err)
	}
	return found
}

func onlyAlert(t *testing.T, event storedEventView) string {
	t.Helper()
	if len(event.Event.RelatedAlertIDs) != 1 {
		t.Fatalf("event %s expected one alert: %+v", event.Event.SourceEventID, event.Processing)
	}
	return event.Event.RelatedAlertIDs[0]
}

func assertNoAlert(t *testing.T, event storedEventView) {
	t.Helper()
	if event.Processing.State != domain.EventProcessStateSuppressed || len(event.Event.RelatedAlertIDs) != 0 {
		t.Fatalf("expected suppressed new event: %+v", event.Processing)
	}
}

func (h *policyHarness) expect(id string, actions ...string) { h.wantActions["linkd-"+id] = actions }

func (h *policyHarness) checkClip() {
	h.t.Log("enabled clip: threshold, active upgrade bypass, terminal cleanup, tenant isolation")
	h.publish("clip", policy.Suppression, map[string]any{"policy": condition("clip"), "scheme": []any{map[string]any{"type": "clip", "count": 3, "duration": 60, "duration_type": "second"}}}, time.Now().Add(10*time.Minute))
	for _, id := range []string{"clip-1", "clip-2"} {
		assertNoAlert(h.t, h.send("clip", "policy-a", id, "same", "clip", "host", "warning", "triggered"))
	}
	counters := h.suppressionWindows("clip", "clip")
	if len(counters) != 1 || counters[0].Count == nil || *counters[0].Count != 2 || counters[0].OwnerAlertID != "" {
		h.t.Fatal("new-event counters missing from runtime API")
	}
	h.assertSuppressionReconcile(counters[0], "unbound_counter", "retained")
	opening := h.send("clip", "policy-a", "clip-3", "same", "clip", "host", "warning", "triggered")
	id := onlyAlert(h.t, opening)
	up := h.send("clip", "policy-a", "clip-up", "same", "clip", "host", "critical", "triggered")
	if up.Processing.PolicyDecision == nil || up.Processing.PolicyDecision.Suppression == nil || up.Processing.PolicyDecision.Suppression.BypassReason != "active_alert" {
		h.t.Fatal("active upgrade did not explicitly bypass new-alert suppression")
	}
	if onlyAlert(h.t, up) != id {
		h.t.Fatal("upgrade replaced opening Alert")
	}
	observed := h.suppressionWindows("clip", "clip")
	if len(observed) != 1 || observed[0].Count == nil || *observed[0].Count != 3 || observed[0].OwnerAlertID != id {
		h.t.Fatal("runtime query/active upgrade counted again or lost owner")
	}
	h.suppressionMembers("clip", observed[0], 3)
	h.assertSuppressionReconcile(observed[0], "active_owner", "retained")
	a := h.alert(id, func(a domain.Alert) bool { return a.Severity == "critical" })
	alertEnrich, alertErr := a.Enrich.Normalize()
	openingEnrich, openingErr := opening.Event.Enrich.Evaluations[0].Data.Normalize()
	if alertErr != nil || openingErr != nil || a.Content != "content-clip-3" || !reflect.DeepEqual(alertEnrich, openingEnrich) {
		h.t.Fatalf("opening facts refreshed: %+v", a)
	}
	h.send("clip", "policy-a", "clip-resolve", "same", "clip", "host", "critical", "resolved")
	terminal := h.alert(id, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered })
	cleanup := h.assertSuppressionCleanup(suppressioncleanup.Cause{TenantID: terminal.BKTenantID, SourceID: terminal.EventSourceID, Fingerprint: terminal.Fingerprint, Trigger: "alert_terminal", AlertID: terminal.AlertID, Revision: terminal.Revision, Status: terminal.Status}, 1, 0)
	if cleanup.Result.Clip.Windows[0].ID != observed[0].ID || cleanup.Result.Clip.Windows[0].Epoch != observed[0].Epoch {
		h.t.Fatal("clip cleanup lost generation")
	}
	if len(h.suppressionWindows("clip", "clip")) != 0 {
		h.t.Fatal("terminal clip counter still listed")
	}
	assertNoAlert(h.t, h.send("clip", "policy-a", "clip-restart", "same", "clip", "host", "warning", "triggered"))
	restarted := h.suppressionWindows("clip", "clip")
	if len(restarted) != 1 || restarted[0].Epoch == counters[0].Epoch || restarted[0].Count == nil || *restarted[0].Count != 1 {
		h.t.Fatal("runtime counter did not restart generation")
	}
	noAlert := h.send("clip", "policy-a", "clip-unbound-resolve", "same", "clip", "host", "warning", "resolved")
	if len(noAlert.Event.RelatedAlertIDs) != 0 {
		h.t.Fatal("unbound resolve created Alert")
	}
	h.assertSuppressionCleanup(suppressioncleanup.Cause{TenantID: noAlert.Event.BKTenantID, SourceID: noAlert.Event.EventSourceID, Fingerprint: noAlert.Event.Fingerprint, Trigger: "event_terminal", EventID: noAlert.Event.EventID}, 1, -1)
	if len(h.suppressionWindows("clip", "clip")) != 0 {
		h.t.Fatal("unbound cleanup retained counter")
	}
	other := h.send("other", "policy-a", "other-first", "same", "clip", "host", "warning", "triggered")
	h.expect(id, "firing", "firing", "resolved")
	h.expect(onlyAlert(h.t, other), "firing")
}

func (h *policyHarness) close(tenant, id string) {
	h.t.Helper()
	h.call(http.MethodPost, "/api/v1/alerts/"+id+"/close", map[string]any{"bk_tenant_id": tenant, "operation_id": "close-" + id, "operator_id": "e2e", "reason": "policy e2e", "effective_at": time.Now().UTC()}, new(any))
	h.alert(id, func(a domain.Alert) bool { return a.Status == domain.AlertStatusClosed })
}

func (h *policyHarness) checkAggregation() {
	h.t.Log("enabled aggregation: cross-source owner and manual-close cleanup")
	h.publish("aggregation", policy.Suppression, map[string]any{"policy": condition("aggregation"), "scheme": []any{map[string]any{"type": "aggregation", "duration": 60, "duration_type": "second", "fields": []string{"object"}}}}, time.Now().Add(10*time.Minute))
	main := onlyAlert(h.t, h.send("aggregation", "policy-a", "agg-main", "a", "aggregation", "shared", "warning", "triggered"))
	child := h.send("aggregation", "policy-b", "agg-child", "b", "aggregation", "shared", "warning", "triggered")
	if child.Processing.State != domain.EventProcessStateSuppressed || onlyAlert(h.t, child) != main {
		h.t.Fatalf("cross-source aggregation did not associate owner: %+v", child.Processing)
	}
	windows := h.suppressionWindows("aggregation", "aggregation")
	if len(windows) != 1 || windows[0].State != "admitted" || windows[0].OwnerAlertID != main || windows[0].MemberCount != 2 {
		h.t.Fatal("aggregation runtime did not retain actual owner/members")
	}
	h.suppressionMembers("aggregation", windows[0], 2)
	h.assertSuppressionReconcile(windows[0], "active_owner", "retained")
	h.close("aggregation", main)
	terminal := h.alert(main, func(a domain.Alert) bool { return a.Status == domain.AlertStatusClosed })
	h.assertSuppressionCleanup(suppressioncleanup.Cause{TenantID: terminal.BKTenantID, SourceID: terminal.EventSourceID, Fingerprint: terminal.Fingerprint, Trigger: "alert_terminal", AlertID: terminal.AlertID, Revision: terminal.Revision, Status: terminal.Status}, 0, 1)
	if len(h.suppressionWindows("aggregation", "aggregation")) != 0 {
		h.t.Fatal("closed aggregation owner still listed")
	}
	// 只在本次隔离 namespace 中重建一个终态 owner 残留，验证实际受控对账，不改写真实 Alert。
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		h.t.Fatal(err)
	}
	redisOptions := cfg.Storage.Redis.ClientOptions()
	redisOptions.ContextTimeoutEnabled = true
	rc, err := redisclient.New(redisOptions)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	state, err := redisstate.New(rc, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		h.t.Fatal(err)
	}
	original := windows[0]
	stale := redisstate.AggregationRequest{Identity: redisstate.Identity{TenantID: terminal.BKTenantID, SourceID: terminal.EventSourceID, Fingerprint: terminal.Fingerprint}, PolicyID: original.Policy.ID, Version: original.Policy.Version, Digest: original.Policy.Digest, GroupKey: original.GroupKey, EventID: "stale-owner-fixture", CandidateAlertID: terminal.AlertID, At: time.Now(), Duration: 60 * time.Second}
	candidate, err := state.ClaimAggregation(h.ctx, stale)
	if err != nil {
		h.t.Fatal(err)
	}
	if ok, err := state.CommitAggregationOwner(h.ctx, stale, candidate); err != nil || !ok {
		h.t.Fatal(ok, err)
	}
	staleWindow, found, err := state.ReadSuppressionWindow(h.ctx, terminal.BKTenantID, "aggregation", candidate.WindowID)
	if err != nil || !found {
		h.t.Fatal(found, err)
	}
	h.assertSuppressionReconcile(staleWindow, "terminal_owner", "cleared")
	if _, found, err := state.ReadSuppressionWindow(h.ctx, terminal.BKTenantID, "aggregation", candidate.WindowID); err != nil || found {
		h.t.Fatal("reconciliation retained stale owner", found, err)
	}
	// 有效占位内尚未创建的候选不能被当作不存在的 owner 强制清理。
	pending := stale
	pending.GroupKey = fmt.Sprintf("%x", sha256.Sum256([]byte("pending-fixture")))
	pending.EventID = "pending-owner-fixture"
	pending.CandidateAlertID = "not-yet-created"
	pending.At = time.Now()
	reserved, err := state.ClaimAggregation(h.ctx, pending)
	if err != nil {
		h.t.Fatal(err)
	}
	pendingWindow, found, err := state.ReadSuppressionWindow(h.ctx, pending.TenantID, "aggregation", reserved.WindowID)
	if err != nil || !found {
		h.t.Fatal(found, err)
	}
	h.assertSuppressionReconcile(pendingWindow, "candidate_pending", "retained")
	if ok, err := state.ReleaseAggregation(h.ctx, pending.TenantID, reserved); err != nil || !ok {
		h.t.Fatal("remove owned pending fixture", ok, err)
	}
	admitted := onlyAlert(h.t, h.send("aggregation", "policy-b", "agg-after-close", "b", "aggregation", "shared", "warning", "triggered"))
	if admitted == main {
		h.t.Fatal("closed owner still owns group")
	}
	h.expect(main, "firing", "close")
	h.expect(admitted, "firing")
}

func (h *policyHarness) suppressionWindows(tenant, kind string) []redisstate.SuppressionWindow {
	h.t.Helper()
	var result struct {
		Tenant string                         `json:"bk_tenant_id"`
		Kind   string                         `json:"kind"`
		Items  []redisstate.SuppressionWindow `json:"items"`
		Next   string                         `json:"next"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/suppression/"+kind+"?bk_tenant_id="+tenant, nil, &result)
	if result.Tenant != tenant || result.Kind != kind || result.Next != "" {
		h.t.Fatal("runtime fixture scope/page incomplete")
	}
	for _, v := range result.Items {
		if v.Validate() != nil || v.TenantID != tenant || v.Kind != kind {
			h.t.Fatal("invalid runtime snapshot")
		}
	}
	return result.Items
}

func (h *policyHarness) suppressionMembers(tenant string, v redisstate.SuppressionWindow, count int) {
	h.t.Helper()
	var result struct {
		Items []redisstate.SuppressionMember `json:"items"`
		Epoch string                         `json:"epoch"`
		Next  string                         `json:"next"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/suppression/"+v.Kind+"/"+url.PathEscape(v.ID)+"/members?"+url.Values{"bk_tenant_id": {tenant}, "epoch": {v.Epoch}}.Encode(), nil, &result)
	if len(result.Items) != count || result.Epoch != v.Epoch || result.Next != "" {
		h.t.Fatal("runtime members missing or wrong generation")
	}
	for _, m := range result.Items {
		if m.EventID == "" || m.SourceID == "" {
			h.t.Fatal("runtime member lost identity")
		}
	}
}

func (h *policyHarness) checkTimeShield() {
	h.t.Log("enabled time shield: real OneModel target and autonomous expiry without Event")
	h.publish("shield", policy.Shield, map[string]any{"policy": condition("shield"), "shield_type": "time_shield", "model_id": "cmdb.host", "target_descriptor": map[string]any{"schema_version": 1, "model_id": "cmdb.host", "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": "cmdb.host", "model_inst_id": "host-1", "entity_uid": "cmdb.host|host-1"}}}}}}, time.Now().Add(25*time.Second))
	id := onlyAlert(h.t, h.send("shield", "policy-a", "shield-first", "s", "shield", "host", "warning", "triggered"))
	bound := h.alert(id, func(a domain.Alert) bool { return a.Shield.Active && a.Admission.AdmittedAt == nil })
	h.assertManualShieldCheck("shield", id, bound.Revision, "retained")
	a := h.alert(id, func(a domain.Alert) bool { return !a.Shield.Active && a.PolicyChange == nil })
	if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
		h.t.Fatal("timer changed lifecycle/admission")
	}
	next := h.send("shield", "policy-a", "shield-next", "s", "shield", "host", "warning", "triggered")
	if onlyAlert(h.t, next) != id {
		h.t.Fatal("unshield replaced Alert")
	}
	h.alert(id, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(id, "firing")
}

func (h *policyHarness) checkMerge() {
	h.t.Log("enabled merge: automatic parent, manual parent close, unlink without action, next Event admission")
	h.publish("merge", policy.Merge, map[string]any{"policy": []any{condition("merge-a"), condition("merge-b")}, "merge_cycle": 45, "is_cycle_merge": false, "aggregate_fields": []string{"object"}, "new_alarm_config": []any{map[string]any{"key": "name", "value": "aggregate ${alarm_num}"}, map[string]any{"key": "content", "value": "members ${alarm_num}"}, map[string]any{"key": "level", "value": "warning"}}}, time.Now().Add(10*time.Minute))
	first := onlyAlert(h.t, h.send("merge", "policy-a", "merge-a-first", "ma", "merge-a", "cluster", "warning", "triggered"))
	waiting := h.alert(first, func(a domain.Alert) bool {
		return a.Merge != nil && len(a.Merge.Pending) == 1 && a.Admission.AdmittedAt == nil
	})
	windowID := waiting.Merge.Pending[0].WindowID
	query := "?bk_tenant_id=merge"
	var window struct {
		ID        string `json:"id"`
		Tenant    string `json:"bk_tenant_id"`
		Committed int    `json:"committed_count"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/merge/windows/"+windowID+query, nil, &window)
	if window.ID != windowID || window.Tenant != "merge" || window.Committed != 1 {
		h.t.Fatalf("runtime window differs from stored wait: %+v", window)
	}
	var windows struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/merge/windows"+query, nil, &windows)
	if len(windows.Items) != 1 || windows.Items[0].ID != windowID {
		h.t.Fatal("runtime window list missed active wait")
	}
	second := onlyAlert(h.t, h.send("merge", "policy-b", "merge-b-first", "mb", "merge-b", "cluster", "warning", "triggered"))
	var parent domain.Alert
	h.until("automatically admitted merge parent", func() bool {
		for _, a := range h.alerts() {
			if a.BKTenantID == "merge" && a.EventSourceID == "builtin_alarm_merge" && a.Merge != nil && a.Merge.RelationsReady && a.MergeChange == nil && a.Admission.AdmittedAt != nil {
				parent = a
				return true
			}
		}
		return false
	})
	if parent.Title != "aggregate 2" || parent.Content != "members 2" {
		h.t.Fatalf("parent template: %+v", parent)
	}
	for _, id := range []string{first, second} {
		a := h.alert(id, func(a domain.Alert) bool {
			return a.Merge != nil && len(a.Merge.RelationIDs) == 1 && a.MergeChange == nil
		})
		if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
			h.t.Fatal("merged child lifecycle/admission changed")
		}
	}
	relationID := parent.Merge.RelationIDs[0]
	point := h.mergeControlPoint("merge", "relations", relationID)
	activeCheck := h.assertMergeRetry(point, "active-relation")
	if activeCheck.Result.Outcome != "unchanged" || activeCheck.Result.Reason != "no_progress" || !activeCheck.Result.StepAttempted {
		h.t.Fatal("active relation check", activeCheck)
	}
	h.close("merge", parent.AlertID)
	endedCheck := h.assertMergeRetry(point, "closed-parent")
	if endedCheck.Result.Outcome != "advanced" && endedCheck.Result.Outcome != "superseded" {
		h.t.Fatal("closed parent check", endedCheck)
	}

	for _, id := range []string{first, second} {
		a := h.alert(id, func(a domain.Alert) bool { return !a.Merge.Blocking() && a.MergeChange == nil })
		if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
			h.t.Fatal("parent close recovered or admitted child")
		}
	}
	// 管理查询保留已完成裁决和历史关系；不能把已清理 Redis 窗口等同于结果丢失。
	var relation domain.MergeRelation
	h.until("ended relation visible through management API", func() bool {
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/relations/"+relationID+query, nil, &relation)
		return relation.State == "ended"
	})
	if relation.ParentAlertID != parent.AlertID || relation.EndReason != "parent_ended" {
		h.t.Fatalf("incorrect relation history: %+v", relation)
	}
	var decisions struct {
		Items []struct {
			ID    string `json:"id"`
			Phase string `json:"phase"`
		} `json:"items"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/merge/decisions"+query+"&phase=completed", nil, &decisions)
	if len(decisions.Items) != 1 || decisions.Items[0].ID != relationID {
		h.t.Fatal("runtime list omitted completed decision")
	}
	var members struct {
		Items []struct {
			Tenant string `json:"bk_tenant_id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/merge/decisions/"+relationID+"/members"+query, nil, &members)
	if len(members.Items) != 2 {
		h.t.Fatal("fixed member snapshots missing")
	}
	for _, member := range members.Items {
		if member.Tenant != "merge" || member.Status != "active" {
			h.t.Fatal("snapshot replaced with current lifecycle")
		}
	}
	var completed mergeflow.ControlPoint
	h.until("completed merge control point", func() bool {
		completed = h.mergeControlPoint("merge", "decisions", relationID)
		return completed.Complete
	})
	already := h.assertMergeRetry(completed, "completed-decision")
	if already.Result.Outcome != "unchanged" || already.Result.Reason != "already_complete" || already.Result.StepAttempted {
		h.t.Fatal("completed decision was restarted", already)
	}
	// 下一条触发改为不匹配合并的 Event；Alert 的 opening title 仍然不刷新。
	next := h.send("merge", "policy-a", "merge-next", "ma", "ordinary", "cluster", "warning", "triggered")
	if onlyAlert(h.t, next) != first {
		h.t.Fatal("next trigger replaced child")
	}
	a := h.alert(first, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	if a.Title != "merge-a" {
		h.t.Fatal("next trigger refreshed opening facts")
	}
	h.expect(parent.AlertID, "firing", "close")
	h.expect(first, "firing")
	h.expect(second)
}

func (h *policyHarness) checkMergeRecoveryAndFailure() {
	h.t.Log("enabled merge: all children recover parent; incomplete window releases original")
	first := onlyAlert(h.t, h.send("merge", "policy-a", "recover-a", "ra", "merge-a", "recover-cluster", "warning", "triggered"))
	second := onlyAlert(h.t, h.send("merge", "policy-b", "recover-b", "rb", "merge-b", "recover-cluster", "warning", "triggered"))
	var parent domain.Alert
	h.until("second admitted merge parent", func() bool {
		for _, a := range h.alerts() {
			if a.BKTenantID == "merge" && a.EventSourceID == "builtin_alarm_merge" && a.Status == domain.AlertStatusActive && a.Merge != nil && a.Merge.RelationsReady && a.MergeChange == nil && a.Admission.AdmittedAt != nil {
				parent = a
				return true
			}
		}
		return false
	})
	h.send("merge", "policy-a", "recover-a-end", "ra", "merge-a", "recover-cluster", "warning", "resolved")
	h.send("merge", "policy-b", "recover-b-end", "rb", "merge-b", "recover-cluster", "warning", "resolved")
	a := h.alert(parent.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered && a.MergeChange == nil })
	if a.EndReason != "merge_members_ended" {
		h.t.Fatalf("unexpected parent recovery: %+v", a)
	}
	h.expect(parent.AlertID, "firing", "resolved")
	h.expect(first)
	h.expect(second)
	single := onlyAlert(h.t, h.send("merge", "policy-a", "single-member", "single", "merge-a", "single-cluster", "warning", "triggered"))
	h.alert(single, func(a domain.Alert) bool {
		return a.Merge != nil && a.Merge.State == "pending" && a.Admission.AdmittedAt == nil
	})
	h.alert(single, func(a domain.Alert) bool {
		return a.Merge != nil && a.Merge.State == "released" && a.MergeChange == nil && a.Admission.AdmittedAt != nil
	})
	h.expect(single, "firing")
}

func (h *policyHarness) checkActionMessages() {
	h.t.Helper()
	topic := h.names.OutputTopic + "-actions"
	request := kmsg.NewPtrListOffsetsRequest()
	request.ReplicaID = -1
	partitions := make(map[int32]kgo.Offset)
	rt := kmsg.NewListOffsetsRequestTopic()
	rt.Topic = topic
	for i := int32(0); i < e2eTopicPartitions; i++ {
		p := kmsg.NewListOffsetsRequestTopicPartition()
		p.Partition = i
		p.Timestamp = -1
		rt.Partitions = append(rt.Partitions, p)
		partitions[i] = kgo.NewOffset().AtStart()
	}
	request.Topics = append(request.Topics, rt)
	response, err := request.RequestWith(h.ctx, h.kafka)
	if err != nil {
		h.t.Fatal(err)
	}
	count := int64(0)
	for _, topic := range response.Topics {
		for _, p := range topic.Partitions {
			if p.ErrorCode != 0 {
				h.t.Fatalf("read action offsets: %d", p.ErrorCode)
			}
			count += p.Offset
		}
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(h.broker), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: partitions}))
	if err != nil {
		h.t.Fatal(err)
	}
	defer consumer.Close()
	got := map[string][]string{}
	for id := range h.wantActions {
		got[id] = nil
	}
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	for received := int64(0); received < count; {
		fetches := consumer.PollFetches(ctx)
		if err := fetches.Err(); err != nil {
			h.t.Fatal(err)
		}
		for _, record := range fetches.Records() {
			var message struct {
				EventID string `json:"event_id"`
				Action  string `json:"action"`
			}
			if err := json.Unmarshal(record.Value, &message); err != nil {
				h.t.Fatal(err)
			}
			got[message.EventID] = append(got[message.EventID], message.Action)
			received++
		}
	}
	if !reflect.DeepEqual(got, h.wantActions) {
		h.t.Fatalf("actual KAC action messages\ngot: %+v\nwant:%+v", got, h.wantActions)
	}
	h.t.Logf("verified %d real KAC action messages; no timer/unlink child action", count)
}

func cleanupPolicyRedis(t *testing.T, client *redis.Client, names resourceNames) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, _ := json.Marshal([]string{"deployment", names.Token})
	patterns := []string{fmt.Sprintf("linkd:merge-requests:%x*", sha256.Sum256([]byte(names.Token))), fmt.Sprintf("linkd:suppression-requests:%x*", sha256.Sum256([]byte(names.Token))), names.LockPrefix + "*", names.SignalStream + "*", "linkd:dispatch:{" + names.Token + "}:*", fmt.Sprintf("linkd:policies:%x:*", sha256.Sum256(raw))}
	for _, pattern := range patterns {
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				t.Errorf("scan owned Redis namespace: %v", err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Errorf("delete owned Redis keys: %v", err)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
}

func cleanupPolicyElasticsearch(t *testing.T, es *elasticsearchClient, names resourceNames) {
	t.Helper()
	cleanupElasticsearch(t, es, names)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, index := range []string{names.IndexPrefix + "_dynamic_config_snapshots", fmt.Sprintf("linkd_event_source_%x_records", sha256.Sum256([]byte(names.Token))), fmt.Sprintf("linkd_event_source_%x_releases", sha256.Sum256([]byte(names.Token)))} {
		if status, err := es.do(ctx, http.MethodDelete, "/"+index, nil, nil); err != nil && status != http.StatusNotFound {
			t.Errorf("delete owned ES index: %v", err)
		}
	}
}

func (h *policyHarness) assertManualShieldCheck(tenant, id string, revision int64, outcome string) {
	h.t.Helper()
	base := "/api/v1/policy-runtime/shield/alerts/" + url.PathEscape(id)
	query := "?bk_tenant_id=" + url.QueryEscape(tenant)
	command := map[string]any{"bk_tenant_id": tenant, "operation_id": "manual-check", "expected_revision": revision, "operator_id": "e2e", "reason": "验证当前屏蔽条件"}
	var accepted shieldcheck.Request
	h.call(http.MethodPost, base+"/reconcile", command, &accepted)
	if accepted.Validate() != nil || accepted.Command.ExpectedRevision != revision {
		h.t.Fatal("invalid accepted check", accepted)
	}
	var final shieldcheck.Request
	h.until("manual shield check finished", func() bool {
		h.call(http.MethodGet, base+"/requests/"+accepted.ID+query, nil, &final)
		return final.State != "pending"
	})
	if final.State != "completed" || final.Result == nil || final.Result.Report.Outcome != outcome || final.Result.Report.Changed || final.Result.Report.ResultRevision != revision {
		h.t.Fatalf("unexpected manual result %+v", final)
	}
	var duplicate shieldcheck.Request
	h.call(http.MethodPost, base+"/reconcile", command, &duplicate)
	if !reflect.DeepEqual(duplicate, final) {
		h.t.Fatal("retry changed completed request")
	}
	h.until("latest shield diagnostic", func() bool {
		var latest struct {
			Check *shieldcheck.Check `json:"check"`
		}
		h.call(http.MethodGet, base+"/check"+query, nil, &latest)
		return latest.Check != nil && latest.Check.Report.Outcome == outcome
	})
	var history struct {
		Items []shieldcheck.Request `json:"items"`
	}
	h.call(http.MethodGet, base+"/requests"+query, nil, &history)
	if len(history.Items) != 1 || history.Items[0].ID != accepted.ID {
		h.t.Fatal("manual history differs", history)
	}
	current := h.alert(id, func(a domain.Alert) bool { return a.Shield.Active && a.Revision == revision })
	if current.Admission.AdmittedAt != nil {
		h.t.Fatal("manual check admitted child")
	}
}

// assertSuppressionCleanup 读取正式持久诊断，不能用 Redis 当前为空替代历史清理证据。
func (h *policyHarness) assertSuppressionCleanup(c suppressioncleanup.Cause, clip, aggregation int) suppressioncleanup.Record {
	h.t.Helper()
	var record suppressioncleanup.Record
	h.call(http.MethodGet, "/api/v1/policy-runtime/suppression/cleanups/"+c.ID()+"?bk_tenant_id="+c.TenantID, nil, &record)
	if record.Validate() != nil || record.Cause != c || record.State != "completed" || record.PreviousUnconfirmed || record.Result.Clip.State != "confirmed" || record.Result.Clip.Removed == nil || *record.Result.Clip.Removed != clip {
		h.t.Fatal("missing confirmed terminal cleanup", record)
	}
	if aggregation < 0 {
		if record.Result.Aggregation.State != "not_applicable" || record.Result.Aggregation.Removed != nil {
			h.t.Fatal("event-only cleanup claimed aggregation")
		}
	} else if record.Result.Aggregation.State != "confirmed" || record.Result.Aggregation.Removed == nil || *record.Result.Aggregation.Removed != aggregation {
		h.t.Fatal("aggregation cleanup result", record)
	}
	return record
}

func (h *policyHarness) assertSuppressionReconcile(w redisstate.SuppressionWindow, reason, outcome string) {
	h.t.Helper()
	base := "/api/v1/policy-runtime/suppression/" + w.Kind + "/" + w.ID
	command := map[string]any{"bk_tenant_id": w.TenantID, "expected_epoch": w.Epoch, "expected_owner_alert_id": w.OwnerAlertID, "operation_id": "check-" + reason, "operator_id": "e2e", "reason": "受控验证既有窗口"}
	var accepted suppressioncheck.Request
	h.call(http.MethodPost, base+"/reconcile", command, &accepted)
	if accepted.Validate() != nil {
		h.t.Fatal("invalid accepted reconciliation", accepted)
	}
	var final suppressioncheck.Request
	h.until("suppression check completed", func() bool {
		h.call(http.MethodGet, base+"/requests/"+accepted.ID+"?bk_tenant_id="+w.TenantID, nil, &final)
		return final.State != "pending"
	})
	if final.Validate() != nil || final.Result.Outcome != outcome || final.Result.Reason != reason || final.Result.Changed != (outcome == "cleared") || final.Command != accepted.Command {
		h.t.Fatal("unexpected reconciliation result", final)
	}
	var replay suppressioncheck.Request
	h.call(http.MethodPost, base+"/reconcile", command, &replay)
	if !reflect.DeepEqual(final, replay) {
		h.t.Fatal("same command reexecuted or changed result")
	}
}

// TestAllInOneMergeRequestsE2E 验证正式请求 API/后台、自动任务竞争、原结果重投和父关闭仅解联。
func TestAllInOneMergeRequestsE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.checkMerge()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

func (h *policyHarness) mergeControlPoint(tenant, kind, id string) mergeflow.ControlPoint {
	h.t.Helper()
	var p mergeflow.ControlPoint
	h.call(http.MethodGet, "/api/v1/policy-runtime/merge/"+kind+"/"+id+"/control?bk_tenant_id="+tenant, nil, &p)
	if p.Validate() != nil || p.TenantID != tenant || p.Kind != kind || p.TargetID != id {
		h.t.Fatal("invalid control point", p)
	}
	return p
}

func (h *policyHarness) assertMergeRetry(p mergeflow.ControlPoint, operation string) mergeflow.RetryRequest {
	h.t.Helper()
	base := "/api/v1/policy-runtime/merge/" + p.Kind + "/" + p.TargetID
	command := map[string]any{"bk_tenant_id": p.TenantID, "expected_token": p.Token, "operation_id": operation, "operator_id": "e2e", "reason": "复核原合并进度"}
	var accepted, final, replay mergeflow.RetryRequest
	h.call(http.MethodPost, base+"/requests", command, &accepted)
	if accepted.Validate() != nil {
		h.t.Fatal("invalid accepted merge request", accepted)
	}
	h.until("merge explicit request finished", func() bool {
		h.call(http.MethodGet, base+"/requests/"+accepted.ID+"?bk_tenant_id="+p.TenantID, nil, &final)
		return final.State != "pending"
	})
	if final.Validate() != nil || final.State == "failed" || final.Command != accepted.Command {
		h.t.Fatal("merge request failed", final)
	}
	h.call(http.MethodPost, base+"/requests", command, &replay)
	if !reflect.DeepEqual(final, replay) {
		h.t.Fatal("merge command replay changed result")
	}
	h.t.Logf("merge request %s: %s/%s", operation, final.Result.Outcome, final.Result.Reason)
	return final
}

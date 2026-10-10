// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package k8sread

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// namespaceAPI serves pod and ReplicaSet lists per namespace, and answers a
// namespace listed in deny with 403 the way RBAC does.
type namespaceAPI struct {
	t           *testing.T
	mu          sync.Mutex
	pods        map[string][]any
	replicaSets map[string][]any
	deny        map[string]bool
	continued   map[string]bool
	lists       map[string]int
}

func (a *namespaceAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method != http.MethodGet {
		a.t.Errorf("a %s was sent to %s: only GET may be", r.Method, r.URL.Path)
	}
	if r.URL.Query().Get("labelSelector") != "" || r.URL.Query().Get("fieldSelector") != "" {
		a.t.Errorf("%s was narrowed by a selector: the namespace read lists everything, bounded only by limit", r.URL.String())
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// api/v1/namespaces/<ns>/pods or apis/apps/v1/namespaces/<ns>/replicasets
	namespace, resource := parts[len(parts)-2], parts[len(parts)-1]
	a.lists[namespace+"/"+resource]++
	if a.deny[namespace] {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "message": resource + ` is forbidden: cannot list resource "` + resource + `" in the namespace "` + namespace + `"`})
		return
	}
	var items []any
	var wantLimit string
	switch resource {
	case "pods":
		items, wantLimit = a.pods[namespace], "200"
	case "replicasets":
		items, wantLimit = a.replicaSets[namespace], "200"
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("limit") != wantLimit {
		a.t.Errorf("%s listed with limit %q, want %s", r.URL.Path, r.URL.Query().Get("limit"), wantLimit)
	}
	body := map[string]any{"items": items, "metadata": map[string]any{}}
	if a.continued[namespace+"/"+resource] {
		body["metadata"] = map[string]any{"continue": "next-page"}
	}
	_ = json.NewEncoder(w).Encode(body)
}

func newNamespaceReader(t *testing.T, api *namespaceAPI, now time.Time, namespaceFile string) *Reader {
	t.Helper()
	api.t = t
	api.lists = map[string]int{}
	server := httptest.NewTLSServer(api)
	t.Cleanup(server.Close)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	dir := t.TempDir()
	must := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	namespacePath := filepath.Join(dir, "missing-namespace")
	if namespaceFile != "" {
		namespacePath = must("namespace", namespaceFile)
	}
	return New(Options{PodName: "alarmd-0", Host: host, Port: port, Client: server.Client(), Now: func() time.Time { return now },
		TokenPath: must("token", "sa-token\n"), CAPath: must("ca.crt", ""), NamespacePath: namespacePath})
}

var readAt = time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)

func ago(d time.Duration) string { return readAt.Add(-d).Format(time.RFC3339) }

func replicaSet(name, deployment, revision string, created time.Duration, images map[string]string, initImages map[string]string) map[string]any {
	var containers, inits []any
	for _, name := range sortedKeys(images) {
		containers = append(containers, map[string]any{"name": name, "image": images[name],
			// A plain env value in the template: the read must never carry it.
			"env": []any{map[string]any{"name": "TOKEN", "value": "SECRET-VALUE"}}})
	}
	for _, name := range sortedKeys(initImages) {
		inits = append(inits, map[string]any{"name": name, "image": initImages[name]})
	}
	return map[string]any{
		"metadata": map[string]any{"name": name, "creationTimestamp": ago(created), "annotations": map[string]string{"deployment.kubernetes.io/revision": revision},
			"ownerReferences": owner("Deployment", deployment)},
		"spec":   map[string]any{"replicas": 2, "template": map[string]any{"spec": map[string]any{"containers": containers, "initContainers": inits}}},
		"status": map[string]any{"readyReplicas": 1},
	}
}

func workloadPodJSON(name, ownerKind, ownerName string, created time.Duration, ready bool, restarts int, image string) map[string]any {
	meta := map[string]any{"name": name, "creationTimestamp": ago(created)}
	if ownerKind != "" {
		meta["ownerReferences"] = owner(ownerKind, ownerName)
	}
	return map[string]any{
		"metadata": meta,
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "main", "image": image, "env": []any{map[string]any{"name": "TOKEN", "value": "SECRET-VALUE"}}}}},
		"status": map[string]any{"startTime": ago(created - time.Second),
			"conditions":        []any{map[string]any{"type": "Ready", "status": map[bool]string{true: "True", false: "False"}[ready]}},
			"containerStatuses": []any{map[string]any{"restartCount": restarts}}},
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// An in-cluster Service address names its namespace; nothing else does, and
// a short <service>.<namespace> that an external name looks the same as is
// not taken for one.
func TestNamespaceOfAddress(t *testing.T) {
	for address, want := range map[string]string{
		"redis-sentinel.storage.svc.cluster.local:26379":             "storage",
		"http://query-http.monitoring.svc.cluster.local:10205/query": "monitoring",
		"https://console.links.svc:8443":                             "links",
		"redis-0.redis-headless.storage.svc.cluster.local:6379":      "storage",
		"kafka.queue.svc":                           "queue",
		"KAFKA.Queue.SVC.cluster.local.":            "queue",
		"kafka.queue.svc.cluster.example.test:9092": "queue",
		"192.0.2.10:6379":                           "",
		"[2001:db8::1]:6379":                        "",
		"redis.example.test:6379":                   "",
		"redis.storage:6379":                        "",
		"svc.bad_name.svc.cluster.local":            "",
		"x_y.ns.svc.cluster.local":                  "",
		"":                                          "",
	} {
		got, ok := NamespaceOfAddress(address)
		if got != want || ok != (want != "") {
			t.Errorf("NamespaceOfAddress(%q) = (%q, %t), want %q", address, got, ok, want)
		}
	}
}

// One namespace's workloads, newest rollout first: a bare Pod started an
// hour ago, a Deployment whose newest ReplicaSet (revision 2, two hours ago)
// changed its container and its init container, and a StatefulSet whose Pod
// is thirty hours old and outside the day's window. No env value from any
// template or Pod reaches the answer.
func TestIsShortServiceName(t *testing.T) {
	for address, want := range map[string]bool{
		"bk-kafka:9092": true, "http://bk-monitor-unify-query-http:10205": true, "redis": true, "Redis.": true,
		"localhost:6379": false, "192.0.2.1:6379": false, "[2001:db8::1]:6379": false, "redis.blueking": false,
		"kafka.ns.svc:9092": false, "bad_name:6379": false, "": false, "https://uq.example.com/": false,
	} {
		if got := IsShortServiceName(address); got != want {
			t.Errorf("IsShortServiceName(%q) = %v, want %v", address, got, want)
		}
	}
}

func TestWorkloadsListsANamespaceNewestRolloutFirstWithWhatTheRolloutChanged(t *testing.T) {
	api := &namespaceAPI{
		pods: map[string][]any{"ns": {
			workloadPodJSON("web-2-a", "ReplicaSet", "web-2", 2*time.Hour, true, 1, "web:2"),
			workloadPodJSON("web-2-b", "ReplicaSet", "web-2", 2*time.Hour, false, 0, "web:2"),
			workloadPodJSON("db-0", "StatefulSet", "db", 30*time.Hour, true, 0, "db:7"),
			workloadPodJSON("debug", "", "", time.Hour, true, 0, "busybox:1"),
		}},
		replicaSets: map[string][]any{"ns": {
			replicaSet("web-1", "web", "1", 48*time.Hour, map[string]string{"web": "web:1"}, map[string]string{"migrate": "migrate:1"}),
			replicaSet("web-2", "web", "2", 2*time.Hour, map[string]string{"web": "web:2"}, map[string]string{"migrate": "migrate:2"}),
		}},
	}
	reader := newNamespaceReader(t, api, readAt, "ns")
	result, err := reader.Workloads(context.Background(), nil, nil, 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Namespaces) != 1 || result.Namespaces[0].Namespace != "ns" ||
		!reflect.DeepEqual(result.Namespaces[0].Origins, []NamespaceOrigin{{Kind: OriginOwn}}) {
		t.Fatalf("namespaces = %+v, want the own namespace alone", result.Namespaces)
	}
	workloads := result.Namespaces[0].Workloads
	var order []string
	for _, workload := range workloads {
		order = append(order, workload.Kind+"/"+workload.Name)
	}
	if want := []string{"Pod/debug", "Deployment/web", "StatefulSet/db"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	web := workloads[1]
	if web.RolloutBasis != RolloutBasisReplicaSet || !web.InWindow || web.LastRolloutAt == nil || !web.LastRolloutAt.Equal(readAt.Add(-2*time.Hour)) {
		t.Fatalf("web rollout = %+v, want the revision 2 ReplicaSet's creation, in the window", web)
	}
	wantChanged := []ImageChange{{Container: "migrate", From: "migrate:1", To: "migrate:2", Init: true}, {Container: "web", From: "web:1", To: "web:2"}}
	if !reflect.DeepEqual(web.Changed, wantChanged) {
		t.Fatalf("web changed = %+v, want %+v", web.Changed, wantChanged)
	}
	if len(web.Rollouts) != 2 || web.Rollouts[0].ReplicaSet != "web-2" || web.Rollouts[0].Revision != "2" || web.Rollouts[0].Desired != 2 || web.Rollouts[0].Ready != 1 {
		t.Fatalf("web rollouts = %+v, want revision 2 then 1", web.Rollouts)
	}
	if web.Pods.Total != 2 || web.Pods.Ready != 1 || web.Pods.Restarts != 1 {
		t.Fatalf("web pods = %+v, want 2 of which 1 ready, 1 restart", web.Pods)
	}
	db := workloads[2]
	if db.RolloutBasis != RolloutBasisPod || db.InWindow || len(db.Changed) != 0 || !reflect.DeepEqual(db.Images, []ContainerImage{{Container: "main", Image: "db:7"}}) {
		t.Fatalf("db = %+v, want its Pod's creation, outside the window, no change it cannot know", db)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "SECRET-VALUE") || strings.Contains(string(encoded), "TOKEN") {
		t.Fatal("an env name or value reached the answer: only names, images and times may")
	}
}

// Every origin is read once and named: the own namespace, one a dependency's
// address names (also listed by the deployment), one only the deployment
// lists and RBAC refuses - reported by name, not as a namespace with no
// workloads - while an address with no namespace and a listed name that is
// not a namespace name are each reported rather than dropped.
func TestWorkloadsNamesEveryNamespaceByItsOriginAndReadsEachOnce(t *testing.T) {
	api := &namespaceAPI{
		pods:        map[string][]any{"ns": {}, "other": {workloadPodJSON("uq-0", "StatefulSet", "uq", time.Hour, true, 0, "uq:3")}},
		replicaSets: map[string][]any{"ns": {}, "other": {}},
		deny:        map[string]bool{"extra": true},
	}
	reader := newNamespaceReader(t, api, readAt, "ns")
	dependencies := []Dependency{
		{Name: "query_backend", Address: "http://uq.other.svc.cluster.local:10205"},
		{Name: "state_redis", Address: "192.0.2.10:6379"},
		{Name: "output_kafka", Address: "kafka.ns.svc:9092"},
		// A bare Service name resolves in this process's own namespace.
		{Name: "strategy_cache", Address: "bk-redis:6379"},
		{Name: "compat_output", Address: "localhost:6379"},
		{Name: "linkd_console", Address: "https://linkd.example.com/console"},
	}
	result, err := reader.Workloads(context.Background(), dependencies, []string{"other", "extra", "Bad_Name", "other"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.WindowHours != DefaultWindowHours {
		t.Fatalf("window = %d, want the default %d for zero", result.WindowHours, DefaultWindowHours)
	}
	got := map[string][]NamespaceOrigin{}
	var order []string
	for _, section := range result.Namespaces {
		got[section.Namespace] = section.Origins
		order = append(order, section.Namespace)
	}
	want := map[string][]NamespaceOrigin{
		"ns": {{Kind: OriginOwn}, {Kind: OriginDerived, Dependency: "output_kafka", Address: "kafka.ns.svc:9092"},
			{Kind: OriginShortName, Dependency: "strategy_cache", Address: "bk-redis:6379"}},
		"other": {{Kind: OriginDerived, Dependency: "query_backend", Address: "http://uq.other.svc.cluster.local:10205"}, {Kind: OriginConfigured}, {Kind: OriginConfigured}},
		"extra": {{Kind: OriginConfigured}},
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(order, []string{"ns", "other", "extra"}) {
		t.Fatalf("origins = %+v in order %v, want %+v in order ns, other, extra", got, order, want)
	}
	for _, key := range []string{"ns/pods", "ns/replicasets", "other/pods", "other/replicasets", "extra/pods", "extra/replicasets"} {
		if api.lists[key] != 1 {
			t.Fatalf("%s listed %d times, want once: lists %v", key, api.lists[key], api.lists)
		}
	}
	extra := result.Namespaces[2]
	if len(extra.Failures) != 2 || extra.Failures[0].Code != CodeForbidden || extra.Failures[1].Code != CodeForbidden || len(extra.Workloads) != 0 {
		t.Fatalf("extra = %+v, want both lists named rbac_forbidden", extra)
	}
	wantUnresolved := []UnresolvedDependency{
		{Dependency: "state_redis", Address: "192.0.2.10:6379", Reason: ReasonNoNamespace},
		{Dependency: "compat_output", Address: "localhost:6379", Reason: ReasonNoNamespace},
		{Dependency: "linkd_console", Address: "https://linkd.example.com/console", Reason: ReasonNoNamespace},
		{Dependency: OriginConfigured, Address: "Bad_Name", Reason: ReasonConfiguredInvalid},
	}
	if !reflect.DeepEqual(result.Unresolved, wantUnresolved) {
		t.Fatalf("unresolved = %+v, want %+v", result.Unresolved, wantUnresolved)
	}
}

// Past a bound the read says so by name: a list with a continuation is cut,
// workloads past MaxWorkloads are cut, and namespaces past MaxNamespaces are
// named and not read.
func TestWorkloadsSaysWhereItWasCut(t *testing.T) {
	var pods []any
	for i := 0; i < MaxWorkloads+1; i++ {
		pods = append(pods, workloadPodJSON("bare-"+strings.Repeat("x", 1)+string(rune('a'+i%26))+string(rune('a'+i/26)), "", "", time.Duration(i)*time.Minute, true, 0, "img:1"))
	}
	api := &namespaceAPI{pods: map[string][]any{"ns": pods}, replicaSets: map[string][]any{"ns": {}}, continued: map[string]bool{"ns/pods": true}}
	reader := newNamespaceReader(t, api, readAt, "ns")
	var listed []string
	for i := 0; i < MaxNamespaces+2; i++ {
		listed = append(listed, "listed-"+string(rune('a'+i)))
	}
	// A namespace past the bound named twice is named once.
	listed = append(listed, listed[len(listed)-1])
	result, err := reader.Workloads(context.Background(), nil, listed, 24)
	if err != nil {
		t.Fatal(err)
	}
	own := result.Namespaces[0]
	if !own.PodsTruncated || !own.WorkloadsTruncated || len(own.Workloads) != MaxWorkloads {
		t.Fatalf("own = pods cut %t, workloads cut %t, %d workloads; want both cut at %d", own.PodsTruncated, own.WorkloadsTruncated, len(own.Workloads), MaxWorkloads)
	}
	if len(result.Namespaces) != MaxNamespaces || len(result.NamespacesTruncated) != 3 {
		t.Fatalf("%d namespaces read, %v cut; want %d read and the other 3 named", len(result.Namespaces), result.NamespacesTruncated, MaxNamespaces)
	}
}

// A process with no ServiceAccount namespace - outside Kubernetes, or with
// nothing mounted - answers the named failure, never an empty list.
func TestWorkloadsWithoutANamespaceFileIsTheNamedFailure(t *testing.T) {
	reader := newNamespaceReader(t, &namespaceAPI{}, readAt, "")
	_, err := reader.Workloads(context.Background(), nil, nil, 24)
	if code := codeOfErr(t, err); code != CodeNoServiceAccount {
		t.Fatalf("code = %s, want %s", code, CodeNoServiceAccount)
	}
}

// The window's edge is in the window; a moment past it is not.
func TestARolloutAtTheWindowsEdgeIsInIt(t *testing.T) {
	api := &namespaceAPI{
		pods:        map[string][]any{"ns": {workloadPodJSON("edge", "", "", 6*time.Hour, true, 0, "a:1"), workloadPodJSON("past", "", "", 6*time.Hour+time.Second, true, 0, "a:1")}},
		replicaSets: map[string][]any{"ns": {}},
	}
	reader := newNamespaceReader(t, api, readAt, "ns")
	result, err := reader.Workloads(context.Background(), nil, nil, 6)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]bool{}
	for _, workload := range result.Namespaces[0].Workloads {
		in[workload.Name] = workload.InWindow
	}
	if !in["edge"] || in["past"] {
		t.Fatalf("in window = %v, want edge in and past out", in)
	}
}

// A rollback re-uses an older ReplicaSet and gives it the next revision: the
// highest revision is the current rollout whatever its creation time, and a
// container the newer rollout dropped is listed as removed. The rollout time
// is still the ReplicaSet's creation - a known limit, said in LastRolloutAt's
// documentation - so a rollback to an old ReplicaSet does not read as recent.
func TestTheHighestRevisionIsTheCurrentRolloutAndARemovedContainerIsListed(t *testing.T) {
	api := &namespaceAPI{
		pods: map[string][]any{"ns": {}},
		replicaSets: map[string][]any{"ns": {
			replicaSet("web-old", "web", "3", 48*time.Hour, map[string]string{"web": "web:1"}, nil),
			replicaSet("web-new", "web", "2", 2*time.Hour, map[string]string{"web": "web:2", "proxy": "proxy:1"}, nil),
		}},
	}
	reader := newNamespaceReader(t, api, readAt, "ns")
	result, err := reader.Workloads(context.Background(), nil, nil, 24)
	if err != nil {
		t.Fatal(err)
	}
	web := result.Namespaces[0].Workloads[0]
	want := []ImageChange{{Container: "web", From: "web:2", To: "web:1"}, {Container: "proxy", From: "proxy:1"}}
	if web.Rollouts[0].ReplicaSet != "web-old" || !reflect.DeepEqual(web.Changed, want) || web.InWindow {
		t.Fatalf("web = current %s, changed %+v, in window %t; want web-old (revision 3), %+v, not in window", web.Rollouts[0].ReplicaSet, web.Changed, web.InWindow, want)
	}
}

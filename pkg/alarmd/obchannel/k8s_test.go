// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/k8sread"
)

// A read that did not happen reaches the CLI as its own failure code, never
// as a complete empty answer, and every answer says the path's boundary.
func TestK8sFailuresReachTheCLIByName(t *testing.T) {
	dir := t.TempDir()
	reader := k8sread.New(k8sread.Options{PodName: "p", Host: "127.0.0.1", Port: "1",
		TokenPath: filepath.Join(dir, "token"), CAPath: filepath.Join(dir, "ca"), NamespacePath: filepath.Join(dir, "ns")})
	ops := K8sOperations(reader, nil, nil)
	ids := map[string]Operation{}
	for _, op := range ops {
		ids[op.ID] = op
	}
	for _, id := range []string{"k8s.pods", "k8s.events", "k8s.logs"} {
		op, ok := ids[id]
		if !ok {
			t.Fatalf("no %s", id)
		}
		out := op.Run(context.Background(), Params{"pod": "p"})
		if out.Error == nil || out.Error.Code != "k8s_"+k8sread.CodeNoServiceAccount || out.Complete || out.Value != nil {
			t.Errorf("%s without a ServiceAccount: %+v", id, out)
		}
		if len(out.Limitations) == 0 || !strings.Contains(out.Limitations[0], "kubectl") {
			t.Errorf("%s does not say its boundary: %v", id, out.Limitations)
		}
		if op.Targetable {
			t.Errorf("%s is answered by the entry replica, not routed to one", id)
		}
	}
	if got := k8sOutcome(nil, errors.New("plain")); got.Error == nil || got.Error.Code != "k8s_"+k8sread.CodeAPIError {
		t.Errorf("an unnamed error is still a failure: %+v", got)
	}
	if got := k8sOutcome(k8sread.EventsResult{}, nil); !got.Complete || got.Error != nil {
		t.Errorf("a read that happened is complete: %+v", got)
	}
}

// The log parameters reach the read as asked.
func TestTheLogParametersReachTheRead(t *testing.T) {
	request := logRequestOf(Params{"pod": "p", "contains": []any{"schedule_cutover", "held_no_open_alert"},
		"scan_lines": json.Number("30000"), "since_seconds": json.Number("600"), "lines": json.Number("50"), "previous": true})
	if request.Pod != "p" || len(request.Contains) != 2 || request.Contains[1] != "held_no_open_alert" ||
		request.ScanLines != 30000 || request.SinceSeconds != 600 || request.Lines != 50 || !request.Previous {
		t.Fatalf("request %+v", request)
	}
	if plain := logRequestOf(Params{"pod": "p"}); plain.Contains != nil || plain.ScanLines != 0 || plain.SinceSeconds != 0 || plain.Lines != k8sread.DefaultLogLines {
		t.Fatalf("plain request %+v", plain)
	}
}

// A scan that stopped short of the end of the log is a partial answer that
// says where it stopped; one that reached the end is complete.
func TestAScanStoppedShortIsPartialAndSaysWhere(t *testing.T) {
	stopped, complete := false, true
	got := logOutcome(k8sread.LogResult{Contains: []string{"schedule_cutover"}, MatchedLines: 3, ScanTo: "2026-09-24T05:00:07Z",
		ScanComplete: &stopped, ScanStopped: k8sread.ScanStoppedDeadline}, nil)
	if got.Complete || len(got.Limitations) != 2 || !strings.Contains(got.Limitations[1], "2026-09-24T05:00:07Z") ||
		!strings.Contains(got.Limitations[1], "deadline") {
		t.Fatalf("stopped scan %+v", got)
	}
	if got := logOutcome(k8sread.LogResult{Contains: []string{"schedule_cutover"}, MatchedLines: 3, ScanComplete: &complete}, nil); !got.Complete {
		t.Fatalf("whole scan %+v", got)
	}
}

// k8s.workloads: without a namespace it is the named failure; with one it
// reads the deployment's list at each call, and a namespace RBAC refuses
// makes the answer partial and says which, beside the read's own boundary.
func TestK8sWorkloadsIsPartialWhereANamespaceWasRefusedAndReadsTheListAtEachCall(t *testing.T) {
	dir := t.TempDir()
	missing := k8sread.New(k8sread.Options{PodName: "p", Host: "127.0.0.1", Port: "1",
		TokenPath: filepath.Join(dir, "token"), CAPath: filepath.Join(dir, "ca"), NamespacePath: filepath.Join(dir, "ns")})
	if out := workloadsOp(t, missing, nil).Run(context.Background(), Params{}); out.Error == nil ||
		out.Error.Code != "k8s_"+k8sread.CodeNoServiceAccount || out.Complete || out.Value != nil {
		t.Fatalf("without a namespace: %+v", out)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/namespaces/refused/") {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "message": "forbidden"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "metadata": map[string]any{}})
	}))
	t.Cleanup(server.Close)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	reader := k8sread.New(k8sread.Options{PodName: "p", Host: host, Port: port, Client: server.Client(),
		TokenPath: write("token", "t"), CAPath: write("ca", ""), NamespacePath: write("ns", "own")})
	listed := []string{}
	op := workloadsOp(t, reader, func() []string { return listed })
	if out := op.Run(context.Background(), Params{}); out.Error != nil || !out.Complete || !strings.Contains(strings.Join(out.Limitations, "\n"), "ALARMD_OBSERVE_NAMESPACES") {
		t.Fatalf("own namespace alone: %+v", out)
	}
	listed = []string{"refused"}
	out := op.Run(context.Background(), Params{})
	result, ok := out.Value.(k8sread.WorkloadsResult)
	if out.Error != nil || out.Complete || !ok || len(result.Namespaces) != 2 ||
		!strings.Contains(strings.Join(out.Limitations, "\n"), "Namespace refused: a list failed") {
		t.Fatalf("with a refused listed namespace: %+v", out)
	}
}

func workloadsOp(t *testing.T, reader *k8sread.Reader, configured func() []string) Operation {
	t.Helper()
	for _, op := range K8sOperations(reader, nil, configured) {
		if op.ID == "k8s.workloads" {
			if op.EvidenceScope != "namespace_workloads" || op.Targetable {
				t.Fatalf("k8s.workloads scope %q targetable %t", op.EvidenceScope, op.Targetable)
			}
			return op
		}
	}
	t.Fatal("no k8s.workloads")
	return Operation{}
}

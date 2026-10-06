// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
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
	ops := K8sOperations(reader)
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

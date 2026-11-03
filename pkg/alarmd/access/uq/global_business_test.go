// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// globalBusinessAttempt is validAttempt as a global business Plan's query:
// the same facts with the global flag set, rebuilt so the revision and the
// spec digest are its own.
func globalBusinessAttempt(t *testing.T) execution.QueryAttempt {
	t.Helper()
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision, facts.GlobalBusiness = "", true
	facts, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	spec := attempt.Spec
	spec.Digest, spec.PlanFacts = "", facts
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

type capturedRequest struct {
	header http.Header
	body   map[string]any
}

func captureRequest(t *testing.T, attempt execution.QueryAttempt) capturedRequest {
	t.Helper()
	var captured capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		captured.header = request.Header.Clone()
		if err := json.NewDecoder(request.Body).Decode(&captured.body); err != nil {
			t.Error(err)
		}
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"is_partial":false}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "alarmd-shadow", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(context.Background(), attempt, &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	return captured
}

// A global business Plan asks the provider to skip the space and names no
// space at all: neither in the header nor in the body, because the provider
// takes the body's space when the header has none, and a named space brings
// its table filters - a shared table's bk_biz_id among them - even with the
// space skipped. The tenant is sent as always: skipping the space does not
// cross tenants.
func TestAGlobalBusinessQuerySkipsTheSpaceAndNamesNone(t *testing.T) {
	got := captureRequest(t, globalBusinessAttempt(t))
	if got.header.Get(headerSkipSpace) == "" {
		t.Fatalf("headers %v carry no %s", got.header, headerSkipSpace)
	}
	if values := got.header.Values(headerSpace); len(values) != 0 {
		t.Fatalf("a global query named the space %v in %s", values, headerSpace)
	}
	if got.header.Get(headerTenant) != "tenant" {
		t.Fatalf("tenant header = %q, want the Plan's tenant", got.header.Get(headerTenant))
	}
	if space, present := got.body["space_uid"]; !present || space != "" {
		t.Fatalf("body space_uid = %v (present %t), want it sent empty", space, present)
	}
}

// The ordinary arm of the same request: the space in both places, and no
// request to skip it.
func TestAnOrdinaryQueryIsScopedToItsSpace(t *testing.T) {
	got := captureRequest(t, validAttempt(t))
	if got.header.Get(headerSpace) != "bkcc__2" || got.body["space_uid"] != "bkcc__2" {
		t.Fatalf("header space %q, body space %v, want bkcc__2 in both", got.header.Get(headerSpace), got.body["space_uid"])
	}
	if values := got.header.Values(headerSkipSpace); len(values) != 0 {
		t.Fatalf("an ordinary query asked to skip the space: %v", values)
	}
}

// The CLI's query preview shows the headers the query is sent with, so a
// global query reads as one there; an ordinary query's preview names the
// four headers it always named.
func TestTheQueryPreviewShowsTheScopeTheQueryIsSentWith(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client, err := NewDiagnosticClient(server.URL, "alarmd-diagnostic", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	global, err := client.Preview(globalBusinessAttempt(t).Spec)
	if err != nil {
		t.Fatal(err)
	}
	if global.Headers[headerSkipSpace] == "" || global.Headers[headerSpace] != "" {
		t.Fatalf("global preview headers = %v, want the skip and no space", global.Headers)
	}
	var body map[string]any
	if err := json.Unmarshal(global.Body, &body); err != nil || body["space_uid"] != "" {
		t.Fatalf("global preview body space_uid = %v (%v), want empty", body["space_uid"], err)
	}
	ordinary, err := client.Preview(validAttempt(t).Spec)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Content-Type": "application/json", headerQuerySource: "alarmd-diagnostic",
		headerTenant: "tenant", headerSpace: "bkcc__2"}
	if len(ordinary.Headers) != len(want) {
		t.Fatalf("ordinary preview headers = %v, want %v", ordinary.Headers, want)
	}
	for name, value := range want {
		if ordinary.Headers[name] != value {
			t.Fatalf("ordinary preview headers = %v, want %v", ordinary.Headers, want)
		}
	}
}

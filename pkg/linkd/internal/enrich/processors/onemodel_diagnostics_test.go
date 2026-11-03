// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/logging"
	"linkd/internal/onemodel"
)

type diagnosticTransport struct {
	body string
	err  error
}

func (r diagnosticTransport) Perform(*http.Request) (*http.Response, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(r.body))}, nil
}

// 从真实 Client 到 Scope、Processor、日志和持久化协议，验证原因码不会被降级边界吞掉。
func TestCMDBAccessDiagnosticReasons(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason, code string
		attrs              string
		body               string
		err                error
	}{
		{name: "query", reason: "query_error", code: "dependency_invalid", err: errors.New("read backend unavailable")},
		{name: "decode", reason: "response_decode_failed", code: "dependency_invalid", body: "{"},
		{name: "multiple", reason: "multiple_instances", code: "dependency_invalid", body: `{"hits":{"hits":[{},{}]}}`},
		{name: "not found", reason: "instance_not_found", code: "missing_field", body: `{"hits":{"hits":[]}}`},
		{name: "field missing", reason: "field_missing", code: "missing_field", attrs: `{}`},
		{name: "field null", reason: "field_empty", code: "missing_field", attrs: `{"bk_biz_name":null}`},
		{name: "field empty", reason: "field_empty", code: "missing_field", attrs: `{"bk_biz_name":""}`},
		{name: "field type", reason: "field_type_invalid", code: "missing_field", attrs: `{"bk_biz_name":2}`},
		{name: "valid", attrs: `{"bk_biz_name":"业务二"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if body == "" {
				body = `{"hits":{"hits":[{"_source":{"bk_tenant_id":"tenant-a","model_id":"cw-biz","model_inst_id":"2","entity_uid":"cw-biz|2","attributes":` + tc.attrs + `}}]}}`
			}
			reader, err := onemodel.NewClient(onemodel.ClientConfig{Transport: diagnosticTransport{body: body, err: tc.err}})
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			logger, err := logging.New(logging.DefaultConfig(), &logs)
			if err != nil {
				t.Fatal(err)
			}
			chain, err := enrich.NewChain([]enrich.Processor{CMDBAccess{}}, enrich.Sources{OneModel: reader}, enrich.WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			event := processorBaseTargetAlert(t, nil)
			event.Labels = domain.DimensionMap{"bk_biz_id": domain.NewStringScalar("2")}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event, Preview: true})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			step := payload.Processors[0]["cmdb-access"]
			if tc.reason == "" {
				if step.Status != domain.EnrichStatusSucceeded || len(step.Diagnostics) != 0 || logs.Len() != 0 {
					t.Fatalf("valid result=%+v logs=%s", step, logs.String())
				}
				return
			}
			if step.Status != domain.EnrichStatusPartial || len(step.Diagnostics) != 1 || len(step.Patches) == 0 {
				t.Fatalf("result=%+v", step)
			}
			d := step.Diagnostics[0]
			if string(d.Code) != tc.code || d.ReasonCode != tc.reason || d.ModelID != "cw-biz" || d.Dependency != "onemodel_instance" {
				t.Fatalf("diagnostic=%+v", d)
			}
			var record struct {
				Diagnostics []enrich.Diagnostic
				Error       string
			}
			if json.Unmarshal(logs.Bytes(), &record) != nil || len(record.Diagnostics) != 1 || record.Diagnostics[0].ReasonCode != tc.reason {
				t.Fatalf("logs=%s", logs.String())
			}
			if tc.err != nil && !strings.Contains(record.Error, tc.err.Error()) {
				t.Fatal("runtime cause lost")
			}
			raw, _ := json.Marshal(result.Data)
			if strings.Contains(string(raw), "backend unavailable") {
				t.Fatal("runtime error persisted")
			}
		})
	}
}

func TestK8sMixedDiagnosticReasons(t *testing.T) {
	t.Parallel()
	reader := newK8sContextReader()
	delete(reader.instances, "cw-K8s_Container")
	v := reader.instances["cw-K8s_Namespace"]
	v.TenantID = "other"
	reader.instances[v.ModelCode] = v
	reader.failed = "cw-K8s_Cluster"
	event := processorBaseTargetAlert(t, k8sPodDimensions())
	event.Dimensions["container_name"] = domain.NewStringScalar("app")
	var logs bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &logs)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{K8s: reader}, enrich.WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	step := payload.Processors[0]["k8s"]
	expected := map[string]string{"cw-K8s_Container": "instance_not_found", "cw-K8s_Namespace": "tenant_mismatch", "cw-K8s_Cluster": "query_error"}
	if step.Status != domain.EnrichStatusPartial || len(step.Diagnostics) != len(expected) {
		t.Fatalf("step=%+v", step)
	}
	for _, d := range step.Diagnostics {
		want, ok := expected[d.ModelID]
		if !ok || d.ReasonCode != want || d.Code != enrich.DiagnosticCodeDependencyInvalid {
			t.Fatalf("diagnostic=%+v", d)
		}
		delete(expected, d.ModelID)
	}
	if len(expected) != 0 || strings.Count(strings.TrimSpace(logs.String()), "\n") != 0 {
		t.Fatalf("missing diagnostics or duplicate log: %v %s", expected, logs.String())
	}
}

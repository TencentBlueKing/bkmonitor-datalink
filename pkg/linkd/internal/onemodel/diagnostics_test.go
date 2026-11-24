package onemodel

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFindInstanceDiagnosticReasons(t *testing.T) {
	t.Parallel()
	valid := map[string]any{"bk_tenant_id": "system", "model_id": "cw-biz", "model_inst_id": "2", "entity_uid": "cw-biz|2", "attributes": map[string]any{}}
	for _, tc := range []struct {
		name, reason string
		change       func(map[string]any)
		body         string
		transportErr error
		invalid      bool
	}{
		{name: "tenant", reason: "tenant_mismatch", change: func(v map[string]any) { v["bk_tenant_id"] = "other" }, invalid: true},
		{name: "model", reason: "model_mismatch", change: func(v map[string]any) { v["model_id"] = "cw-Host" }, invalid: true},
		{name: "empty ID", reason: "instance_id_empty", change: func(v map[string]any) { v["model_inst_id"] = "" }, invalid: true},
		{name: "invalid ID", reason: "instance_id_invalid", change: func(v map[string]any) { v["model_inst_id"] = 2 }, invalid: true},
		{name: "different ID", reason: "instance_id_mismatch", change: func(v map[string]any) { v["model_inst_id"] = "3" }, invalid: true},
		{name: "entity", reason: "entity_uid_mismatch", change: func(v map[string]any) { v["entity_uid"] = "wrong" }, invalid: true},
		{name: "attributes", reason: "attributes_invalid", change: func(v map[string]any) { v["attributes"] = nil }, invalid: true},
		{name: "decode", reason: "response_decode_failed", body: "{", invalid: true},
		{name: "partial", reason: "search_incomplete", body: `{"timed_out":true,"hits":{"hits":[]}}`, invalid: true},
		{name: "multiple", reason: "multiple_instances", body: `{"hits":{"hits":[{},{}]}}`, invalid: true},
		{name: "transport", reason: "query_error", transportErr: errors.New("backend unreachable")},
		{name: "timeout", reason: "query_timeout", transportErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := map[string]any{}
			for k, x := range valid {
				v[k] = x
			}
			if tc.change != nil {
				tc.change(v)
			}
			body := tc.body
			if body == "" {
				raw, _ := json.Marshal(v)
				body = `{"hits":{"hits":[{"_source":` + string(raw) + `}]}}`
			}
			client, err := NewClient(ClientConfig{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := client.FindInstance(t.Context(), "system", InstanceQuery{ModelCode: "cw-biz", InstanceID: "2"})
			if err == nil || found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			// 不依赖错误文案；诊断通过类型化错误跨越包装层传播。
			var reason interface{ DiagnosticReason() string }
			if !errors.As(fmt.Errorf("wrapped: %w", err), &reason) || reason.DiagnosticReason() != tc.reason {
				t.Fatalf("reason=%v want=%s err=%v", reason, tc.reason, err)
			}
			if errors.Is(err, ErrInvalidDataSourceResponse) != tc.invalid {
				t.Fatalf("sentinel changed: %v", err)
			}
			if tc.transportErr != nil && !errors.Is(err, tc.transportErr) {
				t.Fatal("original cause lost")
			}
		})
	}
}

func TestDorisFindInstanceDiagnosticReasons(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason string
		change       func([]driver.Value)
		count        int
	}{
		{"tenant", "tenant_mismatch", func(r []driver.Value) { r[0] = "other" }, 1},
		{"model", "model_mismatch", func(r []driver.Value) { r[1] = "cw-Host" }, 1},
		{"empty ID", "instance_id_empty", func(r []driver.Value) { r[2] = "" }, 1},
		{"wrong ID", "instance_id_mismatch", func(r []driver.Value) { r[2] = "3" }, 1},
		{"entity", "entity_uid_mismatch", func(r []driver.Value) { r[3] = "other|2" }, 1},
		{"decode", "response_decode_failed", func(r []driver.Value) { r[7] = "{" }, 1},
		{"attributes", "attributes_invalid", func(r []driver.Value) { r[7] = "[]" }, 1},
		{"multiple", "multiple_instances", nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := dorisInstanceRow("system", "cw-biz", "2")
			if tc.change != nil {
				tc.change(row)
			}
			client := dorisTestClient(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				items := make([][]driver.Value, tc.count)
				for i := range items {
					items[i] = row
				}
				return &dorisTestRows{columns: instanceColumns, items: items}, nil
			})
			_, found, err := client.FindInstance(t.Context(), "system", InstanceQuery{ModelCode: "cw-biz", InstanceID: "2"})
			if found || !errors.Is(err, ErrInvalidDataSourceResponse) || ErrorReason(err) != tc.reason {
				t.Fatalf("found=%v reason=%s want=%s err=%v", found, ErrorReason(err), tc.reason, err)
			}
		})
	}
}

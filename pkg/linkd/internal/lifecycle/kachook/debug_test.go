package kachook

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
)

func TestDebugFieldsEncodesMetricQueryParams(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "missing"},
		{name: "object", input: `{"expression":"A","query_configs":[{"bk_biz_id":9007199254740993}]}`, want: `{"expression":"A","query_configs":[{"bk_biz_id":9007199254740993}]}`},
		{name: "encoded object", input: `"{\"expression\":\"A\"}"`, want: `{"expression":"A"}`},
		{name: "empty object", input: `{}`, want: `{}`},
		{name: "null", input: `null`, want: `{}`},
		{name: "invalid string", input: `"invalid"`, wantErr: true},
		{name: "array", input: `[]`, wantErr: true},
		{name: "scalar", input: `true`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := domain.Event{ExtraData: domain.JSONObject{}}
			if test.input != "" {
				event.ExtraData["metric_query_params"] = json.RawMessage(test.input)
			}
			fields, err := DebugFields(event, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionResolved}, "source", "source", nil, func(level string) (string, error) { return level, nil })
			if test.wantErr {
				if err == nil {
					t.Fatal("invalid metric query accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			value, exists := fields["metric_query_params"]
			if test.input == "" {
				if exists {
					t.Fatal("missing metric query synthesized")
				}
				return
			}
			if value != test.want {
				t.Fatalf("metric_query_params = %#v, want %q", value, test.want)
			}
		})
	}
}

func TestRecoveryFieldsPreserveHistoricalAndSourceOwnedFields(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		labels string
		extra  string
		url    string
		query  string
		err    bool
	}{
		{name: "historical builtin", source: "built_in_bk", labels: `{"strategy_id":123,"strategy_version":2,"url":"/strategy/42"}`, url: "/strategy/42"},
		{name: "historical builtin query", source: "built_in_bk", labels: `{"strategy_id":123,"strategy_version":2,"url":"/strategy/42"}`, extra: `{"metric_query_params":{"expression":"","query_configs":[{"table":"system.cpu"}]}}`, url: "/strategy/42", query: `{"expression":"","query_configs":[{"table":"system.cpu"}]}`},
		{name: "source URL override", source: "built_in_bk", labels: `{"strategy_id":123,"strategy_version":2,"url":"/strategy/42"}`, extra: `{"field_extra_info":{"strategy_name":{"url":"https://source.example.test/strategy"}}}`, url: "https://source.example.test/strategy"},
		{name: "external fields", source: "external", labels: `{"strategy_id":"opaque"}`, extra: `{"field_extra_info":{"strategy_name":{"url":"https://source.example.test/strategy"}}}`, url: "https://source.example.test/strategy"},
		{name: "invalid builtin reference", source: "built_in_bk", labels: `{"strategy_version":"invalid"}`, err: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := domain.Event{EventSourceID: test.source, BKTenantID: "tenant-a"}
			if err := json.Unmarshal([]byte(test.labels), &event.Labels); err != nil {
				t.Fatal(err)
			}
			if test.extra != "" {
				if err := json.Unmarshal([]byte(test.extra), &event.ExtraData); err != nil {
					t.Fatal(err)
				}
			}
			fields, err := RecoveryFields(event, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionResolved}, test.source, test.source, nil, func(level string) (string, error) { return level, nil })
			if test.err {
				if err == nil {
					t.Fatal("invalid frozen strategy accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info := fields["field_extra_info"].(map[string]any)["strategy_name"].(map[string]any)
			if info["url"] != test.url || info["strategy_context"] != nil {
				t.Fatalf("historical/source strategy fields changed: %#v", info)
			}
			if test.query != "" && fields["metric_query_params"] != test.query {
				t.Fatalf("historical builtin query changed: %#v", fields["metric_query_params"])
			}
		})
	}
}

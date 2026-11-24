package kachook

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
)

func TestDebugFieldsOmitsHistoricalMetricQueryParams(t *testing.T) {
	for _, input := range []string{"", `{"expression":"A"}`, `"invalid"`, `[]`, `null`, `true`} {
		t.Run(input, func(t *testing.T) {
			event := domain.Event{ExtraData: domain.JSONObject{}}
			if input != "" {
				event.ExtraData["metric_query_params"] = json.RawMessage(input)
			}
			before, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := DebugFields(event, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionResolved}, "source", "source", nil, func(level string) (string, error) { return level, nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["metric_query_params"]; exists {
				t.Fatal("historical metric query leaked")
			}
			after, err := json.Marshal(event)
			if err != nil || string(before) != string(after) {
				t.Fatal("projection modified stored event")
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
		err    bool
	}{
		{name: "historical builtin", source: "built_in_bk", labels: `{"strategy_id":123,"strategy_version":2,"url":"/strategy/42"}`, url: "/strategy/42"},
		{name: "historical builtin query", source: "built_in_bk", labels: `{"strategy_id":123,"strategy_version":2,"url":"/strategy/42"}`, extra: `{"metric_query_params":{"expression":"","query_configs":[{"table":"system.cpu"}]}}`, url: "/strategy/42"},
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
			if _, exists := fields["metric_query_params"]; exists {
				t.Fatalf("historical builtin query leaked: %#v", fields["metric_query_params"])
			}
		})
	}
}

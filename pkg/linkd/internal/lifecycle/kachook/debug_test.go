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

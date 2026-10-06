package obevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectionOnlyExposesKnownFieldsAndRedactsNamedCredentials(t *testing.T) {
	raw := []byte(`{"id":7,"items":[{"query_configs":[{"functions":[{"id":"test","params":[{"id":"Authorization","value":"PARAM_SECRET"}]}],"agg_condition":[{"key":"password","method":"eq","value":["CONDITION_SECRET"]},{"key":"bk_host_id","method":"eq","value":["42"]}]}]}],"new_config":{"public_name":"EXTENSION_SECRET"}}`)
	value, omitted, err := projectJSON(raw, sourcePolicy)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(value)
	if strings.Contains(string(data), "SECRET") || !strings.Contains(string(data), `"42"`) {
		t.Fatalf("projection: %s", data)
	}
	if len(omitted) != 3 {
		t.Fatalf("omissions: %+v", omitted)
	}
}

func TestProjectionRejectsNonObjectsAndTrailingDocuments(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `"text"`, `{} {}`, `{`} {
		if _, _, err := projectJSON([]byte(raw), sourcePolicy); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestSourceTargetValuesPreserveOrdinaryMembersButOmitCredentials(t *testing.T) {
	value, omitted, err := projectJSON([]byte(`{"items":[{"target":[[{"key":"Authorization","method":"eq","value":["TARGET_SECRET"]},{"key":"bk_host_id","method":"eq","value":[42]}]]}]}`), sourcePolicy)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(value)
	if strings.Contains(string(data), "TARGET_SECRET") || !strings.Contains(string(data), "42") || len(omitted) != 1 || omitted[0].Reason != "credential_parameter" {
		t.Fatalf("unsafe or lossy target projection: %s %+v", data, omitted)
	}
}

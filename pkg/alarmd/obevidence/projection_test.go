package obevidence

import (
	"bytes"
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

// The writer's effective-time snapshot is readable in the source view -- its
// status and reason decide EFFECTIVE_TIME_SNAPSHOT_* -- and nothing outside
// the compiler's own fields passes.
func TestTheEffectiveTimeSnapshotShowsWhatTheCompilerReads(t *testing.T) {
	raw := []byte(`{"id":379,"effective_time_snapshot":{"schema_version":1,"status":"UNAVAILABLE","reason":"calendar service timeout","business_timezone":"Asia/Shanghai",` +
		`"calendars":[{"id":3,"bk_tenant_id":"system","status":"ok","token":"CAL_SECRET","items":[{"id":9,"time_kind":"repeat","start_time":1,"end_time":2,"time_zone":"Asia/Shanghai",` +
		`"repeat":{"freq":"week","interval":1,"every":[1,2],"exclude_date":[3]}}]}],"extra":"EXTENSION_SECRET"}}`)
	value, omitted, err := projectJSON(raw, sourcePolicy)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(value)
	for _, want := range []string{`"status":"UNAVAILABLE"`, `"reason":"calendar service timeout"`, `"schema_version":1`, `"freq":"week"`, `"every":[1,2]`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("snapshot projection lacks %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), "SECRET") || len(omitted) != 2 {
		t.Fatalf("snapshot projection passed an unknown field: %s %+v", data, omitted)
	}
}

// A Kubernetes static target's match, a model_inst_id rule's model mapping and
// a dynamic group's id read in the source view; a field outside them does not.
func TestTheTargetPlanShowsStaticMatchesAndTheModelMapping(t *testing.T) {
	raw := []byte(`{"items":[{"target_plan":{"schema_version":1,"model_id":"k8s","target_rule":"match","model_match":{"cw_object_model_id":"23","token":"MAP_SECRET"},` +
		`"static_targets":[{"model_id":"k8s","match":{"bcs_cluster_id":"BCS-K8S-00001","namespace":"prod","workload_kind":"Deployment","workload_name":"api","password":"MATCH_SECRET"}}],` +
		`"dynamic_groups":[],"dynamic_topologies":[]}},` +
		`{"target_plan":{"schema_version":1,"model_id":"host","target_rule":"host_id","dynamic_groups":[{"dynamic_group_id":"g1","token":"GROUP_SECRET"}]}}]}`)
	value, omitted, err := projectJSON(raw, sourcePolicy)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(value)
	for _, want := range []string{`"bcs_cluster_id":"BCS-K8S-00001"`, `"workload_name":"api"`, `"cw_object_model_id":"23"`, `"dynamic_groups":[]`, `"dynamic_group_id":"g1"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("target plan projection lacks %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), "SECRET") || len(omitted) != 3 {
		t.Fatalf("target plan projection passed an unknown field: %s %+v", data, omitted)
	}
}

// The published plan freezes each dynamic group as its bare id; the object the
// writer's source spells does not replace it there.
func TestTheFrozenTargetPlanKeepsItsDynamicGroupIDs(t *testing.T) {
	raw := []byte(`{"plans":[{"target_plan":{"schema_version":1,"model_id":"host","target_rule":"host_id","dynamic_groups":["g1","g2"]},` +
		`"target_scope":{"schema_version":1,"model_id":"host","dynamic_groups":["g3"]}}]}`)
	value, omitted, err := projectJSON(raw, publishedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(value)
	for _, want := range []string{`"dynamic_groups":["g1","g2"]`, `"dynamic_groups":["g3"]`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("frozen target plan lacks %s: %s %+v", want, data, omitted)
		}
	}
	if len(omitted) != 0 {
		t.Fatalf("frozen dynamic group ids were omitted: %+v", omitted)
	}
}

// The source view shows a key its policy does not list by its shape: the key,
// every nested key and element, numbers, booleans and nulls as written, and a
// string as its length. A writer's added key - an uptime's cw_calendars - is
// then there to replay against the compiler, and nothing the policy never
// reviewed passes as text. A key named like a credential gives neither value
// nor shape.
func TestTheSourceViewShowsAnUnlistedKeyByItsShape(t *testing.T) {
	raw := []byte(`{"id":54,"webhook_secret":"HOOK_SECRET","runtime_config":{"api_token":"TOKEN_SECRET","depth":3,"on":true,"none":null},` +
		`"detects":[{"level":1,"trigger_config":{"check_window":5,"count":1,"uptime":{"time_ranges":[{"start":"00:00","end":"23:59"}],` +
		`"active_calendars":[3],"cw_calendars":[]}}}],` +
		`"global_scope":{"owner":"team-a","n":2},` +
		`"items":[{"id":1,"query_configs":[{"metric_id":"system.disk.in_use","filter_dict":{"ip":"198.51.100.4","api_token":"FILTER_SECRET"}}]}]}`)
	value, omitted, err := projectSourceJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := plainJSON(t, value)
	// Keys alarmd reads - filter_dict, active_calendars - are shown as
	// written; keys it does not - global_scope, cw_calendars - by shape.
	for _, want := range []string{`"cw_calendars":[]`, `"active_calendars":[3]`, `"filter_dict":{"ip":"198.51.100.4"}`,
		`"global_scope":{"n":2,"owner":"<string of 6 bytes>"}`,
		`"runtime_config":{"api_token":"<credential field>","depth":3,"none":null,"on":true}`, `"metric_id":"system.disk.in_use"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("projection lacks %s: %s", want, text)
		}
	}
	for _, leak := range []string{"SECRET", "team-a", "webhook_secret"} {
		if strings.Contains(text, leak) {
			t.Fatalf("projection shows %s: %s", leak, text)
		}
	}
	reasons := map[string]string{}
	for _, omission := range omitted {
		reasons[omission.Path] = omission.Reason
	}
	for path, reason := range map[string]string{
		"$.webhook_secret": "credential_field", "$.runtime_config": "value_shape_only",
		"$.detects[0].trigger_config.uptime.cw_calendars": "value_shape_only",
		"$.global_scope": "value_shape_only",
		"$.items[0].query_configs[0].filter_dict.api_token": "credential_parameter",
	} {
		if reasons[path] != reason {
			t.Errorf("omission of %s = %q, want %q (all: %+v)", path, reasons[path], reason, omitted)
		}
	}
}

// What the legacy output reads (an item's name) and what an object-model
// target is matched by (the identity pair a query config names, the pair a
// target value holds, a model match under the writer's own dimension) are
// shown as written, not by shape.
func TestTheSourceViewShowsWhatTheOutputAndTheObjectTargetRead(t *testing.T) {
	raw := []byte(`{"id":55,"items":[{"id":1,"name":"disk usage",` +
		`"query_configs":[{"metric_id":"custom.disk","target_identity":{"type":"object_model_inst","object_model_field":"cw_model","object_model_inst_field":"cw_inst"}}],` +
		`"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"23","cw_object_model_inst_id":"7"}]}]],` +
		`"target_plan":{"schema_version":1,"model_id":"23","target_rule":"model_inst_id","model_match":{"cw_model":"23"}}}]}`)
	value, omitted, err := projectSourceJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := plainJSON(t, value)
	for _, want := range []string{`"name":"disk usage"`,
		`"target_identity":{"object_model_field":"cw_model","object_model_inst_field":"cw_inst","type":"object_model_inst"}`,
		`{"cw_object_model_id":"23","cw_object_model_inst_id":"7"}`, `"model_match":{"cw_model":"23"}`} {
		if !strings.Contains(text, want) {
			t.Fatalf("projection lacks %s: %s", want, text)
		}
	}
	if len(omitted) != 0 {
		t.Fatalf("keys alarmd reads were left out or shaped: %+v", omitted)
	}
}

// What the allowlist already redacts stays redacted in the source view, and
// the other views keep leaving unlisted keys out.
func TestTheShapeViewKeepsEveryRedactionAndOnlyTheSourceViewShapes(t *testing.T) {
	raw := []byte(`{"id":7,"items":[{"query_configs":[{"functions":[{"id":"test","params":[{"id":"Authorization","value":"PARAM_SECRET"}]}],"agg_condition":[{"key":"password","method":"eq","value":["CONDITION_SECRET"]},{"key":"bk_host_id","method":"eq","value":["42"]}]}]}],"new_config":{"public_name":"EXTENSION_SECRET"}}`)
	value, omitted, err := projectSourceJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	data := plainJSON(t, value)
	if strings.Contains(data, "SECRET") || !strings.Contains(data, `"42"`) ||
		!strings.Contains(data, `"new_config":{"public_name":"<string of 16 bytes>"}`) {
		t.Fatalf("projection: %s", data)
	}
	credentials := 0
	for _, omission := range omitted {
		if omission.Reason == "credential_parameter" {
			credentials++
		}
	}
	if credentials != 2 {
		t.Fatalf("omissions: %+v, want both named credentials still redacted", omitted)
	}
	published, publishedOmitted, err := projectJSON([]byte(`{"object_contract_version":1,"added_later":{"x":"y"}}`), publishedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(published); strings.Contains(string(encoded), "added_later") ||
		len(publishedOmitted) != 1 || publishedOmitted[0].Reason != "field_not_exposed" {
		t.Fatalf("published view = %s %+v, want the unlisted key left out", encoded, publishedOmitted)
	}
}

func TestACredentialNamedKeyIsKnownByItsName(t *testing.T) {
	for key, want := range map[string]bool{
		"password": true, "db_passwd": true, "webhook_secret": true, "api_token": true, "Authorization": true, "cookie": true,
		"credentials": true, "headers": true, "private_key": true, "basic_auth": true, "pass": true, "passphrase": true, "cert": true, "apiKey": true, "access_key_id": true, "signature": true, "session_id": true,
		"cw_calendars": false, "filter_dict": false, "runtime_config": false, "global_scope": false, "bk_biz_ids": false,
	} {
		if got := credentialFieldName(key); got != want {
			t.Errorf("credentialFieldName(%q) = %v, want %v", key, got, want)
		}
	}
}

// plainJSON encodes without escaping <, > and &, so a shape placeholder reads
// as written.
func plainJSON(t *testing.T, value any) string {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(buffer.String())
}

// A legacy target condition is read by its field and each value's host,
// instance, node or group, and the source view shows them as written.
func TestTheSourceViewShowsATargetConditionAsAlarmdReadsIt(t *testing.T) {
	raw := []byte(`{"id":9,"items":[{"id":1,"target":[[{"field":"bk_target_ip","method":"eq","value":[{"bk_target_ip":"192.0.2.9","bk_target_cloud_id":0}]},` +
		`{"field":"host_topo_node","method":"eq","value":[{"bk_obj_id":"set","bk_inst_id":7}]}]]}]}`)
	value, omitted, err := projectSourceJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := plainJSON(t, value)
	for _, want := range []string{`"field":"bk_target_ip"`, `"bk_target_ip":"192.0.2.9"`, `"bk_target_cloud_id":0`, `"bk_obj_id":"set"`, `"bk_inst_id":7`} {
		if !strings.Contains(text, want) {
			t.Fatalf("projection lacks %s: %s", want, text)
		}
	}
	if len(omitted) != 0 {
		t.Fatalf("a target alarmd reads left omissions: %+v", omitted)
	}
}

package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

// The expected digests were computed by bk-monitor's own functions,
// get_agg_condition_md5 (alarm_backends/service/alert/manager/checker/
// close.py) over count_md5 (bkmonitor/utils/common_utils.py), copied
// verbatim into a scratch script and run with python3 on these lists.
func TestAggConditionMD5MatchesThePlatform(t *testing.T) {
	for _, vector := range []struct{ name, conditions, want string }{
		{"empty", `[]`, "d751713988987e9331980363e24189ce"},
		{"one", `[{"key":"ip","value":["192.0.2.1"],"method":"eq"}]`, "92811130e44a4e5eb434d4dd5acbfdaa"},
		{"one with a first connector", `[{"key":"ip","value":["192.0.2.1"],"method":"eq","condition":"and"}]`, "92811130e44a4e5eb434d4dd5acbfdaa"},
		{"two", `[{"key":"ip","value":["192.0.2.1","192.0.2.2"],"method":"eq"},{"condition":"or","key":"device","value":["eth0"],"method":"neq"}]`,
			"535ab9226fef3171fd744a3d4fc3f603"},
		{"two, first connector changed", `[{"condition":"or","key":"ip","value":["192.0.2.1","192.0.2.2"],"method":"eq"},{"condition":"or","key":"device","value":["eth0"],"method":"neq"}]`,
			"535ab9226fef3171fd744a3d4fc3f603"},
		{"two, second connector changed", `[{"key":"ip","value":["192.0.2.1","192.0.2.2"],"method":"eq"},{"condition":"and","key":"device","value":["eth0"],"method":"neq"}]`,
			"c49ebad2180794dd876ec4565ac72633"},
		{"numbers", `[{"key":"code","value":[200,1.5,100000.0,1e16,0.00001,-3],"method":"eq"}]`, "ae4d3cf2814e8bb61490da37639e52a9"},
		{"other keys ignored", `[{"key":"ip","value":["192.0.2.1"],"method":"eq","dimension_name":"IP"}]`, "92811130e44a4e5eb434d4dd5acbfdaa"},
		{"unicode", `[{"key":"name","value":["中文"],"method":"include"}]`, "55a995a9443091788e434e6de47b7b7b"},
		{"scalar value", `[{"key":"name","value":"x","method":"reg"}]`, "3876dd551988991bbdb280ac2fd7018d"},
		{"bool and null", `[{"key":"flag","value":[true,null,false],"method":"eq"}]`, "fbae7f87302a62dcd8dd4b97160a5de3"},
		{"value order", `[{"key":"ip","value":["192.0.2.2","192.0.2.1"],"method":"eq"}]`, "26706758f34f916c0dbde3d2121dfd33"},
	} {
		got, err := AggConditionMD5(json.RawMessage(vector.conditions))
		if err != nil || got != vector.want {
			t.Errorf("%s: AggConditionMD5 = %q, %v; the platform's is %q", vector.name, got, err, vector.want)
		}
	}
	for _, refused := range []string{`null`, `{}`, `[1]`, `not json`} {
		if _, err := AggConditionMD5(json.RawMessage(refused)); err == nil {
			t.Errorf("agg_condition %s was digested", refused)
		}
	}
}

// The facts of one query config are its metric digest and the platform's
// condition md5; a missing agg_condition reads as an empty one, and a
// query config without a metric_id has no facts.
func TestDeriveStrategyFactsV2(t *testing.T) {
	facts, err := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(
		`{"metric_id":"bk_monitor.system.cpu.usage","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq","condition":"and"}]}`)},
		StrategyGrouping{Fields: []string{"ip", "device"}, NoData: true, NoDataFields: []string{"ip"}})
	if err != nil {
		t.Fatal(err)
	}
	// md5 of ["bk_monitor.system.cpu.usage"]; md5 of ["device","ip"] and of ["ip"].
	if *facts != (StrategyFactsV2{Schema: "v2", MetricDigest: "3815df5a36678b1d738aa6f9a1522549", ConditionMD5: "92811130e44a4e5eb434d4dd5acbfdaa",
		DimensionDigest: "c95842d36b7a76c5ef0fd863a1ea154e", ConditionBasis: "query", NoDataDimensionDigest: "1f99f60df760995e0e77a1169a328a0d"}) {
		t.Fatalf("facts %+v", *facts)
	}
	bare, err := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(`{"metric_id":"bk_monitor.system.cpu.usage"}`)}, StrategyGrouping{Dynamic: true})
	// A dynamic grouping and no no-data state no grouping digest.
	if err != nil || bare.ConditionMD5 != "d751713988987e9331980363e24189ce" || bare.MetricDigest != facts.MetricDigest ||
		bare.DimensionDigest != "" || bare.NoDataDimensionDigest != "" {
		t.Fatalf("facts %+v err %v, want an empty condition and no grouping", bare, err)
	}
	// Two queries: the metric ids in order, the conditions joined in order.
	two, err := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(`{"metric_id":"a","agg_condition":[]}`),
		json.RawMessage(`{"metric_id":"b","agg_condition":[]}`)}, StrategyGrouping{})
	swapped, _ := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(`{"metric_id":"b","agg_condition":[]}`),
		json.RawMessage(`{"metric_id":"a","agg_condition":[]}`)}, StrategyGrouping{})
	// md5 of ["a","b"]; md5 of the two empty-condition md5s joined by ",".
	if err != nil || two.MetricDigest != "e53f04d1bcc1428d9e8db9a93578c5eb" || two.ConditionMD5 != "4a9031297eb2b5f8f7aae3af652c454a" ||
		swapped.MetricDigest == two.MetricDigest {
		t.Fatalf("facts %+v / %+v err %v", two, swapped, err)
	}
	if _, err := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(`{"agg_condition":[]}`)}, StrategyGrouping{}); err == nil {
		t.Fatal("a query config without a metric_id had facts")
	}
}

// The grouping digest is of the key set: order, repeats and a tags. prefix
// do not move it, a key added does. Expected values are the MD5 of the JSON
// array of the sorted key set, computed with python3.
func TestGroupingDigestIsOfTheKeySet(t *testing.T) {
	for _, vector := range []struct {
		fields []string
		want   string
	}{
		{[]string{"ip", "device"}, "c95842d36b7a76c5ef0fd863a1ea154e"},
		{[]string{"device", "ip"}, "c95842d36b7a76c5ef0fd863a1ea154e"},
		{[]string{"tags.device", "ip", "ip"}, "c95842d36b7a76c5ef0fd863a1ea154e"},
		{[]string{"ip", "device", "nic"}, "3902f7d0c455498d7c19a40cdc191bf1"},
		{[]string{"ip"}, "1f99f60df760995e0e77a1169a328a0d"},
		{nil, "d751713988987e9331980363e24189ce"},
	} {
		if got := GroupingDigest(vector.fields); got != vector.want {
			t.Errorf("GroupingDigest(%v) = %s, want %s", vector.fields, got, vector.want)
		}
	}
}

// An event carries the grouping of its own series: the no-data one for a
// no-data event, and never the Plan's other one.
func TestAnEventCarriesItsOwnGrouping(t *testing.T) {
	plan := StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c", ConditionBasis: "query", DimensionDigest: "threshold", NoDataDimensionDigest: "no-data"}
	if got := plan.ForEvent(false); got != (StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c", ConditionBasis: "query", DimensionDigest: "threshold"}) {
		t.Fatalf("threshold event facts %+v", got)
	}
	if got := plan.ForEvent(true); got != (StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c", ConditionBasis: "query", DimensionDigest: "no-data"}) {
		t.Fatalf("no-data event facts %+v", got)
	}
}

// The expected ids and digests were computed by bk-monitor's own
// get_metric_id (bkmonitor/strategy/new_strategy.py), copied verbatim into a
// scratch script and run with python3 -I on these PromQL texts; the digest
// is the MD5 of the JSON array of that one id.
func TestPromQLMetricIDMatchesThePlatform(t *testing.T) {
	long := `sum(container_memory_working_set_bytes{namespace="命名空间",pod=~"服务-.*"}) by (pod) / ` +
		`sum(kube_pod_container_resource_limits{resource="memory",namespace="命名空间"}) by (pod)`
	exactly128 := "rate(up[1m])+rate(up[1m])+rate(up[1m])+rate(up[1m])+rate(up[1m])+rate(up[1m])+rate(up[1m])+rate(up[1m])+rate(up[1m])+rate(up[1m]"
	for _, vector := range []struct{ name, promql, wantID, wantDigest string }{
		{"short", `sum(rate(http_requests_total{job="api"}[5m])) by (instance)`,
			`sum(rate(http_requests_total{job="api"}[5m])) by (instance)`, "0e7fc4ff16c96b3d6a4c23589bd98852"},
		{"exactly 128", exactly128, exactly128, "c52ced342bc0e8d17bde71be7125daed"},
		{"129", strings.Repeat("x", 129), strings.Repeat("x", 125) + "...", "5823b93d3df4ced88a22f240b5d5ad2b"},
		{"long unicode", long, `sum(container_memory_working_set_bytes{namespace="命名空间",pod=~"服务-.*"}) by (pod) / ` +
			`sum(kube_pod_container_resource_limits{reso...`, "d22b9dc7ae4d126e8db1e9aa9a726cba"},
	} {
		if got := PromQLMetricID(vector.promql); got != vector.wantID {
			t.Errorf("%s: PromQLMetricID = %q; the platform's is %q", vector.name, got, vector.wantID)
		}
		query, _ := json.Marshal(map[string]any{"metric_id": "", "data_source_label": "prometheus", "data_type_label": "time_series",
			"promql": vector.promql, "agg_condition": []any{}})
		facts, err := DeriveStrategyFactsV2([]json.RawMessage{query}, StrategyGrouping{})
		if err != nil || facts.MetricDigest != vector.wantDigest {
			t.Errorf("%s: metric digest %+v err %v, want %s", vector.name, facts, err, vector.wantDigest)
		}
	}
	// A query with its own metric_id, or not PromQL, keeps the id as published.
	named, _ := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(
		`{"metric_id":"bk_monitor.system.cpu.usage","data_source_label":"prometheus","data_type_label":"time_series","promql":"up"}`)}, StrategyGrouping{})
	other, _ := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(
		`{"metric_id":"","data_source_label":"bk_monitor","data_type_label":"time_series","promql":"up"}`)}, StrategyGrouping{})
	// md5 of ["bk_monitor.system.cpu.usage"] and of [""].
	if named.MetricDigest != "3815df5a36678b1d738aa6f9a1522549" || other.MetricDigest != "399fc6670871474cd7ce0458401fd299" {
		t.Fatalf("digests %s / %s", named.MetricDigest, other.MetricDigest)
	}
}

// Each query's conditions are read for a basis, and the item's basis is
// the weakest of them.
func TestDeriveStrategyFactsNamesTheConditionBasis(t *testing.T) {
	user := `[{"key":"ip","value":["192.0.2.1"],"method":"eq"}]`
	plain := `{"metric_id":"m","agg_condition":[{"key":"mount_point","method":"eq","value":["/"]}]}`
	injected := `{"metric_id":"m","agg_condition":[{"key":"device_type","method":"neq","value":["tmpfs"]}]}`
	business := `{"metric_id":"m","agg_condition":[{"key":"bk_biz_id","method":"eq","value":["2"]}]}`
	snapshot := `{"metric_id":"m","agg_condition":[{"key":"bk_biz_id","method":"eq","value":["2"]}],"metric_snapshot":{"metric_id":7,"agg_condition":` + user + `}}`
	member := `{"metric_id":"m","agg_condition":[],"metric_snapshot":{"metric_id":7,"agg_condition":[],"derived":{"metric_id":3,"agg_condition":` + user + `}}}`
	for name, vector := range map[string]struct {
		queries []string
		want    string
	}{
		"query":                          {[]string{plain}, "query"},
		"query with device_type":         {[]string{injected}, "query_injected"},
		"query with bk_biz_id":           {[]string{business}, "query_injected"},
		"snapshot":                       {[]string{snapshot}, "snapshot_user"},
		"member":                         {[]string{member}, "snapshot_user"},
		"snapshot without conditions":    {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":{"metric_id":7}}`}, "snapshot_unreadable"},
		"empty snapshot":                 {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":{}}`}, "snapshot_unreadable"},
		"null snapshot conditions":       {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":{"agg_condition":null}}`}, "snapshot_unreadable"},
		"snapshot conditions not a list": {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":{"agg_condition":{"key":"ip"}}}`}, "snapshot_unreadable"},
		"snapshot condition without a method": {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":{"agg_condition":[{"key":"ip","value":["192.0.2.1"]}]}}`},
			"snapshot_unreadable"},
		"member without conditions": {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":{"agg_condition":[],"derived":{"metric_id":3}}}`}, "snapshot_unreadable"},
		"snapshot not an object":    {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":[1]}`}, "snapshot_unreadable"},
		"null snapshot":             {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":null}`}, "query"},
		"snapshot and query":        {[]string{snapshot, plain}, "query"},
		"query and injected":        {[]string{plain, injected}, "query_injected"},
		"injected and unreadable":   {[]string{injected, `{"metric_id":"m","agg_condition":[],"metric_snapshot":{}}`}, "snapshot_unreadable"},
		"two snapshots":             {[]string{snapshot, member}, "snapshot_user"},
		"injected and snapshot":     {[]string{injected, snapshot}, "query_injected"},
		"null derived block":        {[]string{`{"metric_id":"m","agg_condition":[],"metric_snapshot":{"agg_condition":[],"derived":null}}`}, "snapshot_unreadable"},
	} {
		queries := make([]json.RawMessage, 0, len(vector.queries))
		for _, query := range vector.queries {
			queries = append(queries, json.RawMessage(query))
		}
		facts, err := DeriveStrategyFactsV2(queries, StrategyGrouping{})
		if err != nil || facts.ConditionBasis != vector.want {
			t.Errorf("%s: facts %+v err %v, want basis %s", name, facts, err, vector.want)
		}
	}
}

// A catalog metric's conditions are read from its snapshot, the user's, by
// the platform's rule: a change of the merged conditions alone (object_filter
// or member recipe) leaves the digest where it was. The expected digests are
// the golden vectors above: 92811130... for [{key: ip, value: [192.0.2.1],
// method: eq}].
func TestACatalogMetricsConditionsAreTheUsers(t *testing.T) {
	user := `[{"key":"ip","value":["192.0.2.1"],"method":"eq"}]`
	merged := `[{"key":"ip","value":["192.0.2.1"],"method":"eq"},{"condition":"and","key":"model_id","method":"eq","value":["host"]}]`
	refiltered := `[{"key":"ip","value":["192.0.2.1"],"method":"eq"},{"condition":"and","key":"model_id","method":"eq","value":["vm"]}]`
	for name, query := range map[string]string{
		"snapshot":               `{"metric_id":"m","agg_condition":` + merged + `,"metric_snapshot":{"metric_id":7,"agg_condition":` + user + `}}`,
		"snapshot, refiltered":   `{"metric_id":"m","agg_condition":` + refiltered + `,"metric_snapshot":{"metric_id":7,"agg_condition":` + user + `}}`,
		"member":                 `{"metric_id":"m","agg_condition":` + merged + `,"metric_snapshot":{"agg_condition":` + merged + `,"derived":{"agg_condition":` + user + `}}}`,
		"member, recipe changed": `{"metric_id":"m","agg_condition":` + refiltered + `,"metric_snapshot":{"agg_condition":` + refiltered + `,"derived":{"agg_condition":` + user + `}}}`,
	} {
		facts, err := DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(query)}, StrategyGrouping{})
		if err != nil || facts.ConditionMD5 != "92811130e44a4e5eb434d4dd5acbfdaa" || facts.ConditionBasis != "snapshot_user" {
			t.Errorf("%s: facts %+v err %v, want the user's condition digest", name, facts, err)
		}
	}
}

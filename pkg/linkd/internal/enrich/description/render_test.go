package description

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func number(t *testing.T, text string) Number {
	t.Helper()
	n, err := ParseNumber(json.RawMessage(text))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestThresholdGoldenDescriptions(t *testing.T) {
	t.Parallel()
	groups := [][]Condition{{{"gt", 6}, {"lte", 99}, {"neq", 50}}, {{"eq", 6}}}
	for _, tc := range []struct {
		name, value, unit, prefix, want string
		groups                          [][]Condition
	}{
		{"and group", "99", "percent", "%", "avg(测试指标) > 6.0%且 <= 99.0%且 != 50.0%, 当前值99%", groups},
		{"second or group", "6", "percent", "%", "avg(测试指标) = 6.0%, 当前值6%", groups},
		{"first or match wins", "99", "percent", "%", "avg(测试指标) > 6.0%, 当前值99%", [][]Condition{{{"gt", 6}}, {{"gt", 7}}}},
		{"IEC scaling", "1025", "bytes", "Ki", "avg(测试指标) >= 1.0KiB, 当前值1.000977KiB", [][]Condition{{{"gte", 1}}}},
		{"float integer retained", "99.0", "percent", "%", "avg(测试指标) > 6.0%, 当前值99.0%", [][]Condition{{{"gt", 6}}}},
		{"percent fraction", "0.9", "percentunit", "%", "avg(测试指标) >= 80.0%, 当前值90.0%", [][]Condition{{{"gte", 80}}}},
		{"empty unit", "6", "", "", "avg(测试指标) = 6.0, 当前值6", [][]Condition{{{"eq", 6}}}},
		{"custom unit", "6", "次", "", "avg(测试指标) = 6.0次, 当前值6次", [][]Condition{{{"eq", 6}}}},
		{"HTML operator", "6", "percent", "%", "avg(测试指标) < 7.0%, 当前值6%", [][]Condition{{{"lt", 7}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(Facts{ItemName: "avg(测试指标)", Unit: tc.unit, Value: number(t, tc.value), Connector: "and", Algorithms: []Algorithm{{Type: "Threshold", UnitPrefix: tc.prefix, Groups: tc.groups}}})
			if err != nil || got != tc.want {
				t.Fatalf("got %q error %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestSpecialEventGoldenDescriptions(t *testing.T) {
	t.Parallel()
	matched := true
	for _, tc := range []struct {
		name, value, want string
		algorithm         Algorithm
	}{
		{"ping", "1", "Ping不可达", Algorithm{Type: "PingUnreachable"}},
		{"restart", "30", "当前服务器在30秒前发生系统重启事件", Algorithm{Type: "OsRestart", DetectionMatched: &matched}},
		{"restart float", "30.0", "当前服务器在30.0秒前发生系统重启事件", Algorithm{Type: "OsRestart", DetectionMatched: &matched}},
		{"process missing", "0", "当前进程(进程A)不存在", Algorithm{Type: "ProcPort", ProcessName: "进程A"}},
		{"port missing", "1", "当前进程(进程A)存在，端口([80,80-82,443])不存在", Algorithm{Type: "ProcPort", ProcessName: "进程A", NonListeningPorts: []int{443, 82, 80, 81, 80}}},
		{"port zero", "1", "当前进程(进程A)存在，端口([80])不存在", Algorithm{Type: "ProcPort", ProcessName: "进程A", NonListeningPorts: []int{0, 80}}},
		{"IP mismatch", "1", "当前进程(进程A)和监听端口([80])存在，监听的IP与CMDB中配置的(127.0.0.1)不符", Algorithm{Type: "ProcPort", ProcessName: "进程A", InaccurateListen: "[80]", BindIP: "127.0.0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(Facts{Value: number(t, tc.value), Connector: "and", Algorithms: []Algorithm{tc.algorithm}})
			if err != nil || got != tc.want {
				t.Fatalf("got %q err %v want %q", got, err, tc.want)
			}
		})
	}
}

func TestRejectsMissingUnsupportedAndInvalidFacts(t *testing.T) {
	t.Parallel()
	base := Facts{ItemName: "指标", Unit: "percent", Value: number(t, "99"), Connector: "and", Algorithms: []Algorithm{{Type: "Threshold", UnitPrefix: "%", Groups: [][]Condition{{{"gt", 6}}}}}}
	for _, tc := range []struct {
		name, code string
		change     func(*Facts)
	}{
		{"missing value", "facts_invalid", func(f *Facts) { f.Value = Number{} }},
		{"unsupported", "algorithm_unsupported", func(f *Facts) { f.Algorithms = []Algorithm{{Type: "Future"}} }},
		{"empty algorithm list", "facts_invalid", func(f *Facts) { f.Algorithms = nil }},
		{"invalid connector", "connector_invalid", func(f *Facts) { f.Connector = "unknown" }},
		{"invalid category unit", "unit_invalid", func(f *Facts) { f.Unit = "Other||missing" }},
		{"no threshold groups", "threshold_config_invalid", func(f *Facts) { f.Algorithms[0].Groups = nil }},
		{"empty group", "threshold_config_invalid", func(f *Facts) { f.Algorithms[0].Groups = [][]Condition{{}} }},
		{"invalid operator", "threshold_method_invalid", func(f *Facts) { f.Algorithms[0].Groups = [][]Condition{{{"unknown", 6}}} }},
		{"invalid later OR group", "threshold_method_invalid", func(f *Facts) { f.Algorithms[0].Groups = [][]Condition{{{"gt", 6}}, {{"unknown", 6}}} }},
		{"nonfinite", "threshold_config_invalid", func(f *Facts) { f.Algorithms[0].Groups = [][]Condition{{{"gt", math.NaN()}}} }},
		{"scaled value overflow", "unit_overflow", func(f *Facts) { f.Value = number(t, "1e308"); f.Unit = "kbytes" }},
		{"scaled threshold overflow", "unit_overflow", func(f *Facts) {
			f.Unit = "bytes"
			f.Algorithms[0].UnitPrefix = "Ki"
			f.Algorithms[0].Groups = [][]Condition{{{"gt", 1e308}}}
		}},
		{"no hit", "not_triggered", func(f *Facts) { f.Value = number(t, "2") }},
		{"missing restart evidence", "detection_evidence_missing", func(f *Facts) { f.Algorithms = []Algorithm{{Type: "OsRestart"}} }},
		{"invalid port", "process_ports_invalid", func(f *Facts) {
			f.Value = number(t, "1")
			f.Algorithms = []Algorithm{{Type: "ProcPort", NonListeningPorts: []int{65536}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			f.Algorithms = append([]Algorithm(nil), base.Algorithms...)
			tc.change(&f)
			content, err := Render(f)
			var failure *Error
			if content != "" || !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("content=%q err=%v", content, err)
			}
		})
	}
}

func TestAlgorithmConnectorAndInputImmutability(t *testing.T) {
	t.Parallel()
	first := Algorithm{Type: "Threshold", Groups: [][]Condition{{{"gt", 100}}}, UnitPrefix: "%"}
	second := Algorithm{Type: "Threshold", Groups: [][]Condition{{{"gt", 6}}}, UnitPrefix: "%"}
	f := Facts{ItemName: "指标", Unit: "percent", Value: number(t, "99"), Connector: "or", Algorithms: []Algorithm{first, second}}
	got, err := Render(f)
	if err != nil || got != "指标 > 6.0%, 当前值99%" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	f.Connector = "and"
	if _, err := Render(f); err == nil {
		t.Fatal("AND accepted nonmatching algorithm")
	}
	ports := []int{443, 82, 80, 81}
	before := append([]int(nil), ports...)
	if _, err := Render(Facts{Value: number(t, "1"), Connector: "or", Algorithms: []Algorithm{{Type: "ProcPort", NonListeningPorts: ports}}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ports, before) {
		t.Fatal("renderer changed source ports")
	}
}

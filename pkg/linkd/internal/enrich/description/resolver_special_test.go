package description

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

func TestResolverSpecialEventFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, algorithm, metric, value, dimensions, want, code string
	}{
		{name: "missing process", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "0", dimensions: `{"display_name":"nginx"}`, want: "当前进程(nginx)不存在"},
		{name: "non listening ports", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1", dimensions: `{"display_name":"nginx","nonlisten":"[443,80,81]","not_accurate_listen":"[]"}`, want: "当前进程(nginx)存在，端口([80-81,443])不存在"},
		{name: "physical proc metric", algorithm: "ProcPort", metric: "bk_monitor.system.proc_port.proc_exists", value: "1.0", dimensions: `{"display_name":"nginx","nonlisten":"[443]","not_accurate_listen":"[]"}`, want: "当前进程(nginx)存在，端口([443])不存在"},
		{name: "bind mismatch", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1", dimensions: `{"display_name":"nginx","nonlisten":"[]","not_accurate_listen":"[80]","bind_ip":"0.0.0.0"}`, want: "当前进程(nginx)和监听端口([80])存在，监听的IP与CMDB中配置的(0.0.0.0)不符"},
		{name: "missing process name", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "0", dimensions: `{}`, code: "process_evidence_missing"},
		{name: "normal latest point cannot explain trigger", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1", dimensions: `{"display_name":"nginx","nonlisten":"[]","not_accurate_listen":"[]"}`, code: "process_evidence_missing"},
		{name: "invalid process value", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1.5", dimensions: `{"display_name":"nginx"}`, code: "process_facts_invalid"},
		{name: "invalid ports", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1", dimensions: `{"display_name":"nginx","nonlisten":"[65536]"}`, code: "process_facts_invalid"},
		{name: "null ports", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1", dimensions: `{"display_name":"nginx","nonlisten":"null"}`, code: "process_facts_invalid"},
		{name: "numeric ports dimension", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1", dimensions: `{"display_name":"nginx","nonlisten":443}`, code: "process_facts_invalid"},
		{name: "missing bind ip", algorithm: "ProcPort", metric: "bk_monitor.proc_port", value: "1", dimensions: `{"display_name":"nginx","nonlisten":"[]","not_accurate_listen":"[80]"}`, code: "process_evidence_missing"},
		{name: "restart", algorithm: "OsRestart", metric: "bk_monitor.os_restart", value: "120", dimensions: `{}`, want: "当前服务器在120秒前发生系统重启事件"},
		{name: "physical uptime metric", algorithm: "OsRestart", metric: "bk_monitor.system.env.uptime", value: "600", dimensions: `{}`, want: "当前服务器在600秒前发生系统重启事件"},
		{name: "zero uptime", algorithm: "OsRestart", metric: "bk_monitor.os_restart", value: "0", dimensions: `{}`, code: "detection_evidence_missing"},
		{name: "old uptime", algorithm: "OsRestart", metric: "bk_monitor.os_restart", value: "601", dimensions: `{}`, code: "detection_evidence_missing"},
		{name: "year amplitude", algorithm: "YearRoundAmplitude", metric: "plain", value: "99", dimensions: `{}`, code: "detection_evidence_missing"},
		{name: "year range", algorithm: "YearRoundRange", metric: "plain", value: "99", dimensions: `{}`, code: "detection_evidence_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, evaluation, opening, c := resolverFixture(t)
			c.Spec.MonitorItemType = "event"
			c.Queries = []models.StrategyQueryConfig{{MetricID: tc.metric}}
			c.Algorithms = []RuntimeAlgorithm{{Type: tc.algorithm, Level: 2}}
			if err := json.Unmarshal([]byte(tc.dimensions), &event.Dimensions); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(`{"value":`+tc.value+`}`), &event.Values); err != nil {
				t.Fatal(err)
			}
			event.SourceRawData["values"] = json.RawMessage(`{"value":` + tc.value + `}`)
			before, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return c, nil }))
			if err != nil {
				t.Fatal(err)
			}
			builder, err := NewBuilder(resolver)
			if err != nil {
				t.Fatal(err)
			}
			got, err := builder.BuildContent(t.Context(), event, evaluation, opening)
			if tc.code != "" {
				var failure *Error
				if !errors.As(err, &failure) || failure.Code != tc.code {
					t.Fatalf("error=%v want=%s", err, tc.code)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("content=%q error=%v want=%q", got, err, tc.want)
			}
			after, err := json.Marshal(event)
			if err != nil || string(after) != string(before) {
				t.Fatal("description changed source Event")
			}
		})
	}
}

func TestResolverProcPortBoundsAndSpecialBinding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*domain.Event, *Configuration)
		code   string
	}{
		{"oversized name", func(e *domain.Event, _ *Configuration) {
			e.Dimensions["display_name"] = domain.NewStringScalar(strings.Repeat("n", 4097))
		}, "process_facts_invalid"},
		{"oversized ports", func(e *domain.Event, _ *Configuration) {
			e.Dimensions["nonlisten"] = domain.NewStringScalar("[" + strings.Repeat("80,", 2048) + "80]")
		}, "process_facts_invalid"},
		{"wrong algorithm", func(_ *domain.Event, c *Configuration) { c.Algorithms[0].Type = "Threshold" }, "special_query_rewrite_unverified"},
		{"ambiguous algorithm", func(_ *domain.Event, c *Configuration) { c.Algorithms = append(c.Algorithms, c.Algorithms[0]) }, "special_query_rewrite_unverified"},
		{"ambiguous query", func(_ *domain.Event, c *Configuration) { c.Queries = append(c.Queries, c.Queries[0]) }, "special_query_rewrite_unverified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, evaluation, opening, c := resolverFixture(t)
			event.Dimensions = domain.DimensionMap{"display_name": domain.NewStringScalar("nginx"), "nonlisten": domain.NewStringScalar("[80]"), "not_accurate_listen": domain.NewStringScalar("[]")}
			event.Values["value"] = 1
			event.SourceRawData["values"] = json.RawMessage(`{"value":1}`)
			c.Queries = []models.StrategyQueryConfig{{MetricID: "bk_monitor.proc_port"}}
			c.Algorithms = []RuntimeAlgorithm{{Type: "ProcPort", Level: 2}}
			tc.change(&event, &c)
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return c, nil }))
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.Resolve(t.Context(), event, evaluation, opening)
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("error=%v want=%s", err, tc.code)
			}
		})
	}
}

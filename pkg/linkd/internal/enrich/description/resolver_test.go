package description

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

type configurationReaderFunc func(context.Context, ConfigurationQuery) (Configuration, error)

func (f configurationReaderFunc) ReadConfiguration(ctx context.Context, q ConfigurationQuery) (Configuration, error) {
	return f(ctx, q)
}

func resolverFixture(t *testing.T) (domain.Event, domain.EventEvaluation, domain.Alert, Configuration) {
	t.Helper()
	evaluation := domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered}
	event := domain.Event{BKTenantID: "tenant", EventSourceID: "source", EventSourceVersion: 7, EventID: "event", Fingerprint: "group", Evaluations: []domain.EventEvaluation{evaluation}, Values: domain.EventValues{"value": 99}, SourceRawData: domain.JSONObject{"values": json.RawMessage(`{"value":99}`)}, Labels: domain.DimensionMap{}}
	for key, value := range map[string]float64{"strategy_id": 1, "strategy_version": 1790758008036099, "bk_biz_id": 2} {
		scalar, err := domain.NewNumberScalar(value)
		if err != nil {
			t.Fatal(err)
		}
		event.Labels[key] = scalar
	}
	opening := domain.Alert{BKTenantID: "tenant", EventSourceID: "source", EventSourceVersion: 7, TriggerEventID: "event", Fingerprint: "group", Severity: "warning"}
	configuration := Configuration{Identity: ConfigurationQuery{"tenant", 1, 1790758008036099, 2}, Spec: models.CWStrategySpec{Name: "CPU", StrategyItem: &models.CWStrategyItem{AggregateMethod: "AVG", Connector: "and", QueryConfigs: []models.StrategyQueryConfig{{}}}}, Queries: []models.StrategyQueryConfig{{Unit: "percent"}}, Algorithms: []RuntimeAlgorithm{{Type: "Threshold", Level: 1, UnitPrefix: "%", Config: json.RawMessage(`[[{"method":"gt","threshold":100}]]`)}, {Type: "Threshold", Level: 2, UnitPrefix: "%", Config: json.RawMessage(`[[{"method":"gt","threshold":80}]]`)}}}
	return event, evaluation, opening, configuration
}

func TestResolverRendersOnlySelectedLevelWithPublishedUnit(t *testing.T) {
	t.Parallel()
	event, evaluation, opening, configuration := resolverFixture(t)
	resolver, err := NewResolver(configurationReaderFunc(func(ctx context.Context, q ConfigurationQuery) (Configuration, error) {
		if ctx != t.Context() || q != configuration.Identity {
			t.Fatal("wrong identity/context")
		}
		return configuration, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := resolver.Resolve(t.Context(), event, evaluation, opening)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(facts)
	if err != nil || got != "AVG(CPU) > 80.0%, 当前值99%" {
		t.Fatalf("content=%q error=%v", got, err)
	}
}

func TestResolverRejectsUnprovenFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		change     func(*domain.Event, *Configuration)
	}{
		{"missing value", "observed_value_missing", func(e *domain.Event, _ *Configuration) { delete(e.Values, "value") }},
		{"missing raw type", "observed_number_type_missing", func(e *domain.Event, _ *Configuration) { e.SourceRawData = nil }},
		{"changed value", "observed_value_mismatch", func(e *domain.Event, _ *Configuration) { e.Values["value"] = 100 }},
		{"wrong configuration identity", "configuration_identity_mismatch", func(_ *domain.Event, c *Configuration) { c.Identity.TenantID = "other" }},
		{"history", "detection_evidence_missing", func(_ *domain.Event, c *Configuration) { c.Algorithms[1].Type = "SimpleRingRatio" }},
		{"special query rewrite", "special_query_rewrite_unverified", func(_ *domain.Event, c *Configuration) { c.Queries[0].MetricID = "bk_monitor.ping-gse" }},
		{"process", "process_evidence_missing", func(_ *domain.Event, c *Configuration) { c.Algorithms[1].Type = "ProcPort" }},
		{"ambiguous unit", "query_unit_ambiguous", func(_ *domain.Event, c *Configuration) {
			c.Queries = append(c.Queries, models.StrategyQueryConfig{Unit: "s"})
		}},
		{"observed unit", "observed_unit_mismatch", func(e *domain.Event, _ *Configuration) {
			e.ExtraData = domain.JSONObject{"unit": json.RawMessage(`"s"`)}
		}},
		{"wrong family", "detection_evidence_missing", func(e *domain.Event, _ *Configuration) {
			e.ExtraData = domain.JSONObject{"evaluation_family": json.RawMessage(`"nodata"`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, evaluation, opening, configuration := resolverFixture(t)
			tc.change(&event, &configuration)
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
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

func TestResolverPreservesWireNumberType(t *testing.T) {
	t.Parallel()
	event, _, _, _ := resolverFixture(t)
	event.Values["value"] = 10
	for _, raw := range []string{"10", "10.0"} {
		event.SourceRawData["values"] = json.RawMessage(`{"value":` + raw + `}`)
		value, err := eventObservedNumber(event)
		if err != nil || value.integer != (raw == "10") {
			t.Fatalf("raw=%s value=%v error=%v", raw, value, err)
		}
	}
}

func TestResolverDependencyAndCancellation(t *testing.T) {
	t.Parallel()
	event, evaluation, opening, _ := resolverFixture(t)
	dependency := errors.New("unavailable")
	calls := 0
	resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) {
		calls++
		return Configuration{}, dependency
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(t.Context(), event, evaluation, opening); !errors.Is(err, dependency) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolver.Resolve(ctx, event, evaluation, opening); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(err)
	}
	if _, err := NewResolver(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestConfigurationThresholdAcceptsPublishedNumericStrings(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`[[{"method":"gte","threshold":"90"}]]`, `[[{"method":"gte","threshold":90}]]`} {
		groups, err := configurationThreshold(json.RawMessage(raw))
		if err != nil || len(groups) != 1 || len(groups[0]) != 1 || groups[0][0].Threshold != 90 {
			t.Fatalf("groups=%v error=%v", groups, err)
		}
	}
	for _, raw := range []string{`[[{"method":"gte","threshold":"NaN"}]]`, `[[{"method":"gte","threshold":null}]]`, `[[{"method":"gte","threshold":true}]]`} {
		if _, err := configurationThreshold(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid threshold accepted")
		}
	}
}

func TestResolverPingUsesOriginalLossValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, content string }{
		{"unreachable", "1", "Ping不可达"},
		{"loss float", "1.0", "Ping不可达"},
		{"reachable", "0.5", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, evaluation, opening, configuration := resolverFixture(t)
			number, err := ParseNumber(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			event.Values["value"] = number.value
			event.SourceRawData["values"] = json.RawMessage(`{"value":` + tc.raw + `}`)
			configuration.Spec.MonitorItemType = "event"
			configuration.Spec.StrategyItem.AggregateMethod = ""
			configuration.Spec.StrategyItem.QueryConfigs = nil
			configuration.Queries = []models.StrategyQueryConfig{{MetricID: "bk_monitor.ping-gse", Unit: "percentunit"}}
			configuration.Algorithms = []RuntimeAlgorithm{{Type: "PingUnreachable", Level: 2}}
			resolver, err := NewResolver(configurationReaderFunc(func(context.Context, ConfigurationQuery) (Configuration, error) { return configuration, nil }))
			if err != nil {
				t.Fatal(err)
			}
			facts, err := resolver.Resolve(t.Context(), event, evaluation, opening)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Render(facts)
			if content != tc.content || ((err != nil) != (tc.content == "")) {
				t.Fatalf("content=%q error=%v", content, err)
			}
		})
	}
}

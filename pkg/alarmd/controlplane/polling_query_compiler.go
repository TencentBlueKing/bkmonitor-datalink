package controlplane

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func pollingQueryDelay(source PrimaryQuerySource, configs []legacyQueryConfig, interval int64) (int64, error) {
	delay := source.TimeDelaySeconds
	if delay < 0 || interval <= 0 || delay > (1<<63-1)-interval {
		return 0, queryConfigRejected("QUERY_DELAY_INVALID", nil)
	}
	if delay == 0 {
		for _, c := range configs {
			if c.DataSourceLabel == "bk_log_search" && c.DataTypeLabel == "log" {
				delay = 60
				break
			}
		}
	}
	return (delay + interval - 1) / interval * interval, nil
}

func pollingSourceSupported(c legacyQueryConfig) bool {
	switch c.DataSourceLabel + "/" + c.DataTypeLabel {
	case "bk_monitor/time_series", "custom/time_series", "prometheus/time_series", "bk_data/time_series",
		"bk_log_search/time_series", "bk_log_search/log", "bk_monitor/log", "custom/event", "bk_fta/event":
		return true
	}
	return false
}

func freezePollingNormalization(n execution.DatasetNormalizationSpec, sources []string) (execution.DatasetNormalizationSpec, error) {
	// Source-specific field mapping and dynamic identity are part of the frozen
	// dataset identity, even when two providers happen to emit identical clauses.
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-polling-normalization-v1", struct {
		Normalization execution.DatasetNormalizationSpec
		Sources       []string
	}{n, sources})
	if err != nil {
		return n, err
	}
	n.DatasetContract.NormalizationDigest = digest
	n.DatasetContract.SchemaDigest, err = contract.DeriveCanonicalDigestV2("alarmd-polling-schema-v1", n.DatasetContract)
	return n, err
}

func (compiler *LegacyPrimaryQueryCompiler) compilePromQL(source PrimaryQuerySource, c legacyQueryConfig) (execution.QueryPlanFacts, error) {
	if c.PromQL == "" || len(source.Functions) > 0 || len(source.IdentityFields) > 0 {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_PROMQL_CONFIG_INVALID", nil)
	}
	interval := c.AggInterval
	if interval == 0 {
		interval = 60
	}
	if interval > (1<<63-1)/1000 {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_INTERVAL_INVALID", nil)
	}
	match, err := promQLMatch(c.FilterDict)
	if err != nil {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_PROMQL_MATCH_INVALID", err)
	}
	n, err := buildLegacyUQNormalization([]string{})
	if err != nil {
		return execution.QueryPlanFacts{}, err
	}
	n.DatasetContract.DynamicDimensions = true
	sources := []string{"prometheus/time_series"}
	n, err = freezePollingNormalization(n, sources)
	if err != nil {
		return execution.QueryPlanFacts{}, err
	}
	delay, err := pollingQueryDelay(source, []legacyQueryConfig{c}, interval)
	if err != nil {
		return execution.QueryPlanFacts{}, err
	}
	return execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		QueryDelaySeconds: delay,
		Provider:          execution.ProviderUQ, ProviderRouteRef: compiler.providerRoute,
		TenantID: source.Identity.TenantID, BusinessID: source.Identity.BusinessID, SpaceScope: source.Identity.SpaceScope,
		PromQL: &execution.PromQLQuery{Expression: c.PromQL, Match: match}, SourceSemantics: sources,
		StepMillis: interval * 1000, AlignmentMillis: interval * 1000, DownSampleRange: execution.DownSampleNone,
		Timezone: compiler.timezone, Normalization: n,
	})
}

func promQLMatch(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", err
	}
	var parts []string
	var visit func(map[string]any)
	visit = func(m map[string]any) {
		keys := make([]string, 0, len(m))
		for key := range m {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			switch value := m[key].(type) {
			case map[string]any:
				visit(value)
			case string:
				// Python's repr chooses single quotes unless doing so needs an
				// otherwise unnecessary escape. UQ accepts both quoted forms.
				quoted := strconv.Quote(value)
				if !strings.Contains(value, "'") {
					quoted = "'" + strings.ReplaceAll(quoted[1:len(quoted)-1], "\\\"", "\"") + "'"
				}
				parts = append(parts, key+"="+quoted)
			}
		}
	}
	visit(fields)
	return "{" + strings.Join(parts, ",") + "}", nil
}

func monitorEventField(field string) string {
	switch field {
	case "target", "event_name", "event.content", "event.count", "time", "_index":
		return field
	}
	return "dimensions." + field
}

func (compiler *LegacyPrimaryQueryCompiler) compilePollingQueryConfig(c legacyQueryConfig, business string) ([]execution.QueryClause, error) {
	c.AggDimensions = append([]string{}, c.AggDimensions...)
	c.AggConditions = append([]legacyCondition{}, c.AggConditions...)
	if c.AggInterval > (1<<63-1)/1000 {
		return nil, queryConfigRejected("QUERY_INTERVAL_INVALID", nil)
	}
	if c.DataSourceLabel == "bk_fta" {
		return compiler.compileFTAQuery(c, business)
	}
	if c.DataSourceLabel != "bk_log_search" && c.DataTypeLabel == "time_series" {
		if c.DataSourceLabel == "bk_data" && c.TimeField == "" {
			c.TimeField = "dtEventTimeStamp"
		}
		return compiler.compileLegacyQueryConfig(c)
	}
	if c.AggInterval == 0 {
		c.AggInterval = 60
	}
	c.Values = nil
	if c.DataSourceLabel == "bk_log_search" {
		c.ResultTableID = "bklog_index_set_" + c.ResultTableID
		if strings.Contains(c.QueryString, "__dist_05") {
			c.ResultTableID += "_clustered"
		}
		if c.TimeField == "" {
			c.TimeField = "dtEventTimeStamp"
		}
	} else {
		c.TimeField = "time"
		for i, field := range c.AggDimensions {
			c.AggDimensions[i] = monitorEventField(field)
		}
		for i, condition := range c.AggConditions {
			c.AggConditions[i].Key = monitorEventField(condition.Key)
		}
		if c.DataSourceLabel == "custom" {
			c.MetricField, c.AggMethod = "_index", "COUNT"
			if c.CustomEventName != "" {
				c.AggConditions = append(c.AggConditions, scalarCondition("event_name", "eq", c.CustomEventName))
			}
			// Constructor-injected recovery filtering is a dimensions field.
			c.AggConditions = append(c.AggConditions, scalarCondition("dimensions.event_type", "neq", "RECOVERY"))
		} else {
			c.MetricField = "event.count"
			if strings.EqualFold(c.AggMethod, "COUNT") {
				c.AggMethod = "SUM"
			}
		}
		if c.ResultTableID != "system_event" && c.ResultTableID != "k8s_event" && c.ResultTableID != "cicd_event" {
			c.ResultTableID += ".__default__"
		}
		c.Alias = "a"
	}
	if c.QueryString == "" {
		c.QueryString = "*"
	}
	if c.MetricField == "" {
		c.MetricField = "_index"
	}
	// Log operators differ from the time-series compatibility mapping.
	conditions, err := compileLogConditions(c.AggConditions)
	if err != nil {
		return nil, queryConfigRejected("QUERY_CONDITION_INVALID", err)
	}
	clauses, err := compiler.compileLegacyQueryConfig(c)
	if err != nil {
		return nil, err
	}
	for i := range clauses {
		clauses[i].Conditions = conditions
		clauses[i].KeepColumns = []string{}
		clauses[i].DataSource = "bkapm"
		if c.DataSourceLabel == "bk_log_search" {
			clauses[i].DataSource = "bklog"
		}
		method := strings.ToLower(c.AggMethod)
		if strings.HasPrefix(method, "cp") && len(clauses[i].Functions) > 0 {
			percent, err := strconv.Atoi(strings.TrimPrefix(method, "cp"))
			if err != nil {
				return nil, err
			}
			clauses[i].Functions[0].Method = "percentiles"
			clauses[i].Functions[0].Arguments = []execution.QueryScalar{{Kind: execution.QueryScalarNumber, NumberValue: strconv.Itoa(percent)}}
			clauses[i].TimeAggregation.Method = method + "_over_time"
		}
	}
	return clauses, nil
}

func scalarCondition(field, method, value string) legacyCondition {
	raw, _ := json.Marshal([]string{value})
	return legacyCondition{Key: field, Method: method, Value: raw, Connector: "and"}
}

func compileLogConditions(source []legacyCondition) (execution.QueryConditions, error) {
	result, err := compileConditions(source)
	if err != nil {
		return result, err
	}
	mapping := map[string]string{"reg": "req", "nreg": "nreq", "neq": "ne", "exists": "existed", "nexists": "nexisted", "include": "contains", "exclude": "ncontains"}
	for i, original := range source {
		result.Fields[i].Operator = original.Method
		if op := mapping[original.Method]; op != "" {
			result.Fields[i].Operator = op
		}
	}
	return result, nil
}

func (compiler *LegacyPrimaryQueryCompiler) compileFTAQuery(c legacyQueryConfig, business string) ([]execution.QueryClause, error) {
	if compiler.runtimeFacts.FTAEventStorage == nil {
		return nil, querySourceIncomplete("QUERY_FTA_EVENT_STORAGE_FACT_MISSING", nil)
	}
	storage := compiler.runtimeFacts.FTAEventStorage
	if c.AggInterval == 0 {
		c.AggInterval = 60
	}
	// The Python FTA datasource floors seconds to whole minutes before bucketing.
	c.AggInterval = c.AggInterval / 60 * 60
	if c.AggInterval == 0 {
		return nil, queryConfigRejected("QUERY_FTA_INTERVAL_INVALID", nil)
	}
	name := c.MetricField
	c.MetricField, c.AggMethod, c.TimeField, c.ResultTableID = "_index", "COUNT", storage.TimeField.Name, storage.TableID
	if c.Alias == "" {
		c.Alias = "_index"
	}
	c.AggConditions = append(c.AggConditions, scalarCondition("status", "eq", "ABNORMAL"))
	if business != "0" {
		c.AggConditions = append(c.AggConditions, scalarCondition("bk_biz_id", "eq", business))
	}
	switch {
	case name == "__ALL_EVENT_PLUGIN__":
		c.AggConditions = append(c.AggConditions, scalarCondition("plugin_id", "neq", "bkmonitor"))
	case strings.HasPrefix(name, "__EVENT_PLUGIN__"):
		c.AggConditions = append(c.AggConditions, scalarCondition("plugin_id", "eq", strings.TrimPrefix(name, "__EVENT_PLUGIN__")))
	case name != "":
		c.AggConditions = append(c.AggConditions, scalarCondition("alert_name.raw", "eq", name))
	}
	conditions, err := compileLogConditions(c.AggConditions)
	if err != nil {
		return nil, fmt.Errorf("FTA conditions: %w", err)
	}
	clauses, err := compiler.compileLegacyQueryConfig(c)
	if err != nil {
		return nil, err
	}
	for i := range clauses {
		clauses[i].Driver = "influxdb"
		clauses[i].FieldSemantics = "fta_event_tags/v1"
		clauses[i].Conditions = conditions
		clauses[i].KeepColumns = nil
	}
	return clauses, nil
}

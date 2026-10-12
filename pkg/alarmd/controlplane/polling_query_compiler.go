package controlplane

import (
	"encoding/json"
	"html"
	"regexp"
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

// pollingSourceSupported answers whether a query config's data source can be
// compiled at all. The set it reads is SupportedSourceSemantics, which is
// also what the Catalog composition reports Query Groups under: one list, so
// a source that starts compiling cannot go unnamed in the composition, and a
// source named there cannot be one nothing compiles.
func pollingSourceSupported(c legacyQueryConfig) bool {
	semantics := c.DataSourceLabel + "/" + c.DataTypeLabel
	for _, supported := range SupportedSourceSemantics {
		if semantics == supported {
			return true
		}
	}
	return false
}

func freezePollingNormalization(n execution.DatasetNormalizationSpec, sources []string) (execution.DatasetNormalizationSpec, error) {
	n.Version = "uq-polling-normalization-v1"
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
		Timezone: compiler.timezone, Normalization: n, GlobalBusiness: source.Identity.GlobalBusiness,
	})
}

// promQLMatch renders a filter_dict as the promql match string the provider
// takes beside the expression, the way the backend renders it: string values
// in Python's repr quoting, nested maps flattened, values of any other type
// skipped. Keys are sorted here and not there. The sort is not for parity --
// matcher order has no meaning to the provider -- it is what makes the match
// string, and through it the query revision the objects are addressed by,
// deterministic across rounds; removing it to match the backend's map order
// would recut every promql Query Group's revision on every refresh.
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
	if c.DataSourceLabel != "bk_log_search" && c.DataTypeLabel == "time_series" {
		if c.DataSourceLabel == "bk_data" && c.TimeField == "" {
			c.TimeField = "dtEventTimeStamp"
		}
		clauses, err := compiler.compileLegacyQueryConfig(c, metricConditions)
		if c.DataSourceLabel == "bk_data" {
			for i := range clauses {
				clauses[i].DataSource = "bkdata"
			}
		}
		return clauses, err
	}
	if c.AggInterval == 0 {
		c.AggInterval = 60
	}
	c.Values = nil
	c.DataLabel = ""
	if c.DataSourceLabel == "bk_log_search" {
		indexSet, err := pythonStringScalar(c.IndexSetID)
		if err != nil || indexSet == "" || indexSet == "None" {
			return nil, queryConfigRejected("QUERY_LOG_INDEX_SET_MISSING", err)
		}
		c.ResultTableID = "bklog_index_set_" + indexSet
		c.QueryString = logSearchQueryString(c.QueryString)
		clustered := strings.Contains(c.QueryString, "__dist_05")
		for _, condition := range c.AggConditions {
			if strings.HasPrefix(condition.Key, "__dist") {
				clustered = true
			}
		}
		if clustered {
			c.ResultTableID += "_clustered"
		}
		if c.TimeField == "" {
			c.TimeField = "dtEventTimeStamp"
		}
		if c.MetricField == "" {
			// A log query without a field counts documents, whatever method the
			// strategy names: LogSearchTimeSeriesDataSource.init_by_query_config
			// takes COUNT on _index when metric_field is empty, and the log
			// source inherits it. Kept as the strategy's AVG, the provider is
			// asked to average _index, which the log store refuses.
			c.MetricField, c.AggMethod = "_index", "COUNT"
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
			c.AggConditions = append(c.AggConditions, scalarCondition("dimensions.event_type", "neq", "recovery"))
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
	clauses, err := compiler.compileLegacyQueryConfig(c, logConditions)
	if err != nil {
		return nil, err
	}
	for i := range clauses {
		clauses[i].KeepColumns = []string{}
		clauses[i].DataSource = "bkapm"
		if c.DataSourceLabel == "bk_log_search" {
			clauses[i].DataSource = "bklog"
		}
		method := strings.ToLower(c.AggMethod)
		if len(clauses[i].Functions) > 0 {
			switch method {
			case "avg":
				clauses[i].Functions[0].Method = "avg"
			case "distinct":
				clauses[i].Functions[0].Method = "cardinality"
			}
		}
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

var logSearchSpecial = regexp.MustCompile(`[-+=&|><!(){}\[\]^"~*?:/]|AND|OR|TO|NOT`)

func logSearchQueryString(raw string) string {
	value := html.UnescapeString(raw)
	if strings.TrimSpace(value) == "" {
		return "*"
	}
	if !logSearchSpecial.MatchString(value) {
		return "*" + value + "*"
	}
	return value
}

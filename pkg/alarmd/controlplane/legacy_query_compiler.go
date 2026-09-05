package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const legacyUQNormalizationVersion = "uq-threshold-normalization-v1"

var timeShiftPattern = regexp.MustCompile(`^([+-]?)([0-9]*\.?[0-9]+)([smhdw])$`)

// QueryPlanCompileError retains the stable control-plane disposition without
// exposing the Legacy DTO to downstream packages.
type QueryPlanCompileError struct {
	Disposition Disposition
	Reason      string
	Err         error
}

func (failure *QueryPlanCompileError) Error() string {
	if failure == nil {
		return "alarmd controlplane: query plan compilation failed"
	}
	if failure.Err != nil {
		return fmt.Sprintf("alarmd controlplane: %s: %v", failure.Reason, failure.Err)
	}
	return "alarmd controlplane: " + failure.Reason
}

func (failure *QueryPlanCompileError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

type LegacyPrimaryQueryCompiler struct {
	providerRoute execution.ProviderRouteRef
	timezone      string
	runtimeFacts  LegacyQueryRuntimeFacts
}

type LegacyRuntimeFilterFact struct {
	FieldName string
	Values    []string
}

// LegacyQueryRuntimeFacts freezes the effective Python settings which are not
// present in a cached strategy document. A nil slice or pointer means that the
// fact was not observed; an explicitly empty slice is a valid observed value.
type LegacyQueryRuntimeFacts struct {
	AccessBKData          *bool
	BKDataCMDBLevelTables []string
	SystemDiskFilter      LegacyRuntimeFilterFact
	SystemNetworkFilter   LegacyRuntimeFilterFact
}

func NewLegacyPrimaryQueryCompiler(providerRoute execution.ProviderRouteRef, timezone string, runtimeFacts LegacyQueryRuntimeFacts) (*LegacyPrimaryQueryCompiler, error) {
	if providerRoute == "" || timezone == "" {
		return nil, errors.New("alarmd controlplane: provider route and timezone are required")
	}
	runtimeFacts.BKDataCMDBLevelTables = cloneStringsPreservingNil(runtimeFacts.BKDataCMDBLevelTables)
	runtimeFacts.SystemDiskFilter.Values = cloneStringsPreservingNil(runtimeFacts.SystemDiskFilter.Values)
	runtimeFacts.SystemNetworkFilter.Values = cloneStringsPreservingNil(runtimeFacts.SystemNetworkFilter.Values)
	if runtimeFacts.AccessBKData != nil {
		accessBKData := *runtimeFacts.AccessBKData
		runtimeFacts.AccessBKData = &accessBKData
	}
	return &LegacyPrimaryQueryCompiler{providerRoute: providerRoute, timezone: timezone, runtimeFacts: runtimeFacts}, nil
}

func cloneStringsPreservingNil(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

type legacyQueryConfig struct {
	DataSourceLabel string            `json:"data_source_label"`
	DataTypeLabel   string            `json:"data_type_label"`
	MetricID        string            `json:"metric_id"`
	MetricField     string            `json:"metric_field"`
	Alias           string            `json:"alias"`
	Values          []string          `json:"values"`
	AggDimensions   []string          `json:"agg_dimension"`
	AggMethod       string            `json:"agg_method"`
	AggConditions   []legacyCondition `json:"agg_condition"`
	AggInterval     int64             `json:"agg_interval"`
	ResultTableID   string            `json:"result_table_id"`
	TimeField       string            `json:"time_field"`
	QueryString     string            `json:"query_string"`
	Functions       []legacyFunction  `json:"functions"`
	DataLabel       string            `json:"data_label"`
	FilterDict      json.RawMessage   `json:"filter_dict"`
}

type legacyCondition struct {
	Connector string          `json:"condition"`
	Key       string          `json:"key"`
	Method    string          `json:"method"`
	Value     json.RawMessage `json:"value"`
}

type legacyFunction struct {
	ID     string                `json:"id"`
	Params []legacyFunctionParam `json:"params"`
}

type legacyFunctionParam struct {
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
}

type legacyMetric struct {
	Field  string
	Method string
	Alias  string
}

func (compiler *LegacyPrimaryQueryCompiler) CompilePrimaryQuery(_ context.Context, source PrimaryQuerySource) (execution.QueryPlanFacts, error) {
	if compiler == nil || compiler.providerRoute == "" || compiler.timezone == "" {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_COMPILER_UNINITIALIZED", nil)
	}
	if err := source.Identity.validate(); err != nil || source.StrategyID == "" || source.ItemID == "" || source.QueryMD5 == "" || len(source.QueryConfigs) == 0 {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_SOURCE_INCOMPLETE", err)
	}
	configs := make([]legacyQueryConfig, 0, len(source.QueryConfigs))
	for _, raw := range source.QueryConfigs {
		config, err := decodeLegacyQueryConfig(raw)
		if err != nil {
			return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_CONFIG_INVALID", err)
		}
		if config.DataSourceLabel != "bk_monitor" || config.DataTypeLabel != "time_series" {
			return execution.QueryPlanFacts{}, queryUnsupported("QUERY_SOURCE_NOT_MIGRATED", nil)
		}
		config.AggDimensions = canonicalDimensionStrings(config.AggDimensions)
		// Python alarm Access does not pass cached query_config.filter_dict to
		// TimeSeriesDataSource.init_by_query_config. Only the runtime filters
		// below participate in the effective UQ request.
		configs = append(configs, config)
	}

	queryList := make([]execution.QueryClause, 0, len(configs))
	identitySet := make(map[string]struct{})
	stepSeconds := int64(0)
	for _, config := range configs {
		clauses, err := compiler.compileLegacyQueryConfig(config)
		if err != nil {
			return execution.QueryPlanFacts{}, err
		}
		queryList = append(queryList, clauses...)
		for _, dimension := range config.AggDimensions {
			if dimension != "" {
				identitySet[dimension] = struct{}{}
			}
		}
		if config.AggInterval > 0 && (stepSeconds == 0 || config.AggInterval < stepSeconds) {
			stepSeconds = config.AggInterval
		}
	}
	if stepSeconds == 0 {
		stepSeconds = 60
	}
	identityFields := make([]string, 0, len(identitySet))
	for field := range identitySet {
		identityFields = append(identityFields, field)
	}
	sort.Strings(identityFields)
	if source.IdentityFields != nil {
		if !sortedUniqueNonEmpty(source.IdentityFields) {
			return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_IDENTITY_FIELDS_INVALID", nil)
		}
		for _, field := range source.IdentityFields {
			if _, ok := identitySet[field]; !ok {
				return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_IDENTITY_FIELD_NOT_RETURNED", nil)
			}
		}
		identityFields = append([]string{}, source.IdentityFields...)
	}
	metricMerge, err := compileMetricMerge(source.Expression, source.Functions, queryList)
	if err != nil {
		return execution.QueryPlanFacts{}, err
	}
	normalization, err := buildLegacyUQNormalization(identityFields)
	if err != nil {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_NORMALIZATION_INVALID", err)
	}
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider:         execution.ProviderUQ,
		ProviderRouteRef: compiler.providerRoute,
		TenantID:         source.Identity.TenantID,
		BusinessID:       source.Identity.BusinessID,
		SpaceScope:       source.Identity.SpaceScope,
		QueryList:        queryList,
		MetricMerge:      metricMerge,
		StepMillis:       stepSeconds * 1000,
		AlignmentMillis:  stepSeconds * 1000,
		DownSampleRange:  execution.DownSampleNone,
		Timezone:         compiler.timezone,
		NotTimeAlign:     false,
		Normalization:    normalization,
	})
	if err != nil {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_FACTS_INVALID", err)
	}
	return facts, nil
}

func (compiler *LegacyPrimaryQueryCompiler) CompileAlgorithmDependencyQuery(
	ctx context.Context,
	source PrimaryQuerySource,
	expression string,
) (execution.QueryPlanFacts, error) {
	if expression == "" {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_DEPENDENCY_EXPRESSION_INVALID", nil)
	}
	source.Expression = expression
	source.Functions = nil
	return compiler.CompilePrimaryQuery(ctx, source)
}

func sortedUniqueNonEmpty(values []string) bool {
	for index, value := range values {
		if value == "" || (index > 0 && value <= values[index-1]) {
			return false
		}
	}
	return true
}

func decodeLegacyQueryConfig(raw json.RawMessage) (legacyQueryConfig, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var config legacyQueryConfig
	if len(raw) == 0 || string(raw) == "null" {
		return config, errors.New("query config must be an object")
	}
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	if config.AggInterval < 0 {
		return config, errors.New("query interval is invalid")
	}
	return config, nil
}

func (compiler *LegacyPrimaryQueryCompiler) compileLegacyQueryConfig(config legacyQueryConfig) ([]execution.QueryClause, error) {
	dimensions := append([]string{}, config.AggDimensions...)
	conditionsSource := append([]legacyCondition(nil), config.AggConditions...)
	if filter, matched, err := compiler.runtimeFilter(config); err != nil {
		return nil, err
	} else if matched && len(filter.Values) > 0 {
		conditionsSource, err = mergeRuntimeFilterConditions(filter, conditionsSource)
		if err != nil {
			return nil, queryConfigRejected("QUERY_RUNTIME_FILTER_INVALID", err)
		}
	}
	if isCMDBLevelQuery(config) {
		if compiler.runtimeFacts.AccessBKData == nil {
			return nil, querySourceIncomplete("QUERY_ACCESS_BK_DATA_FACT_MISSING", nil)
		}
		if *compiler.runtimeFacts.AccessBKData {
			if compiler.runtimeFacts.BKDataCMDBLevelTables == nil {
				return nil, querySourceIncomplete("QUERY_CMDB_LEVEL_TABLE_FACT_MISSING", nil)
			}
			if containsString(compiler.runtimeFacts.BKDataCMDBLevelTables, config.ResultTableID) {
				return nil, queryUnsupported("QUERY_CMDB_LEVEL_BYPASSES_UQ", nil)
			}
			config.ResultTableID = strings.SplitN(config.ResultTableID, "_cmdb_level", 2)[0]
			config.TimeField = "dtEventTimeStamp"
		}
	}
	conditions, err := compileConditions(conditionsSource)
	if err != nil {
		return nil, queryConfigRejected("QUERY_CONDITION_INVALID", err)
	}
	method := config.AggMethod
	if method == "" {
		method = "COUNT"
	}
	metrics := make([]legacyMetric, 0, len(config.Values)+1)
	if config.MetricField != "" {
		alias := config.Alias
		if alias == "" {
			alias = config.MetricField
		}
		metrics = append(metrics, legacyMetric{Field: config.MetricField, Method: method, Alias: alias})
	} else {
		alias := config.Alias
		if alias == "" {
			alias = "alias"
		}
		metrics = append(metrics, legacyMetric{Field: "_index", Method: "COUNT", Alias: alias})
	}
	for _, field := range config.Values {
		if field == "" {
			return nil, queryConfigRejected("QUERY_VALUE_FIELD_INVALID", nil)
		}
		metrics = append(metrics, legacyMetric{Field: field, Method: method, Alias: field})
	}
	queryFunctions := append([]legacyFunction(nil), config.Functions...)
	offset, offsetForward, err := extractTimeShift(&queryFunctions)
	if err != nil {
		return nil, queryConfigRejected("QUERY_TIME_SHIFT_INVALID", err)
	}
	timeField := config.TimeField
	if timeField == "" {
		timeField = "time"
	}
	table := config.DataLabel
	if table == "" {
		table = strings.ToLower(config.ResultTableID)
	}
	clauses := make([]execution.QueryClause, 0, len(metrics))
	for _, metric := range metrics {
		functions, timeAggregation, err := compileAggregation(metric.Method, config.AggInterval, dimensions)
		if err != nil {
			return nil, err
		}
		timeAggregation, extraFunctions, err := compileQueryFunctions(queryFunctions, timeAggregation, dimensions)
		if err != nil {
			return nil, err
		}
		functions = append(functions, extraFunctions...)
		reference := strings.ToLower(metric.Alias)
		clauses = append(clauses, execution.QueryClause{
			Driver:          "influxdb",
			TableID:         table,
			FieldName:       metric.Field,
			TimeField:       timeField,
			ReferenceName:   reference,
			Functions:       functions,
			TimeAggregation: timeAggregation,
			Dimensions:      append([]string(nil), dimensions...),
			Conditions:      conditions,
			Offset:          offset,
			OffsetForward:   strconv.FormatBool(offsetForward),
			KeepColumns:     append([]string{"_time", reference}, dimensions...),
			QueryString:     config.QueryString,
		})
	}
	return clauses, nil
}

func mergeRuntimeFilterConditions(filter LegacyRuntimeFilterFact, source []legacyCondition) ([]legacyCondition, error) {
	value, err := json.Marshal(filter.Values)
	if err != nil {
		return nil, err
	}
	groups := make([][]legacyCondition, 0, 1)
	current := make([]legacyCondition, 0, len(source))
	for _, condition := range source {
		if condition.Connector == "or" && len(current) > 0 {
			groups = append(groups, current)
			current = nil
		}
		condition.Connector = "and"
		current = append(current, condition)
	}
	if len(current) > 0 || len(groups) == 0 {
		groups = append(groups, current)
	}
	result := make([]legacyCondition, 0, len(source)+len(groups))
	for index, group := range groups {
		connector := "and"
		if index > 0 {
			connector = "or"
		}
		result = append(result, legacyCondition{Connector: connector, Key: filter.FieldName, Method: "neq", Value: value})
		result = append(result, group...)
	}
	return result, nil
}

func (compiler *LegacyPrimaryQueryCompiler) runtimeFilter(config legacyQueryConfig) (LegacyRuntimeFilterFact, bool, error) {
	var fact LegacyRuntimeFilterFact
	switch config.ResultTableID {
	case "system.disk":
		fact = compiler.runtimeFacts.SystemDiskFilter
	case "system.net":
		fact = compiler.runtimeFacts.SystemNetworkFilter
	default:
		return LegacyRuntimeFilterFact{}, false, nil
	}
	if fact.FieldName == "" || fact.Values == nil {
		return LegacyRuntimeFilterFact{}, true, querySourceIncomplete("QUERY_RUNTIME_FILTER_FACT_MISSING", nil)
	}
	return fact, true, nil
}

func isCMDBLevelQuery(config legacyQueryConfig) bool {
	for _, dimension := range config.AggDimensions {
		if dimension == "bk_obj_id" || dimension == "bk_inst_id" {
			return true
		}
	}
	for _, condition := range config.AggConditions {
		if condition.Key == "bk_obj_id" || condition.Key == "bk_inst_id" {
			return true
		}
	}
	return false
}

func compileAggregation(rawMethod string, interval int64, dimensions []string) ([]execution.QueryFunction, execution.QueryFunction, error) {
	if rawMethod == "" || rawMethod == "REAL_TIME" {
		return []execution.QueryFunction{}, execution.QueryFunction{}, nil
	}
	method := strings.ToLower(rawMethod)
	arguments := []execution.QueryScalar(nil)
	position := int32(0)
	switch method {
	case "cp50", "cp90", "cp95", "cp99":
		quantiles := map[string]string{"cp50": "0.5", "cp90": "0.9", "cp95": "0.95", "cp99": "0.99"}
		arguments = []execution.QueryScalar{{Kind: execution.QueryScalarNumber, NumberValue: quantiles[method]}}
		position = 1
		method = "quantile"
	case "sum_without_time", "avg_without_time", "count_without_time", "min_without_time", "max_without_time":
		method = strings.TrimSuffix(method, "_without_time")
		if method == "avg" {
			method = "mean"
		}
		return []execution.QueryFunction{{Method: method, Dimensions: append([]string(nil), dimensions...)}}, execution.QueryFunction{}, nil
	}
	window := interval
	if window == 0 {
		window = 3600
	}
	queryMethod := method
	if queryMethod == "avg" {
		queryMethod = "mean"
	} else if queryMethod == "count" {
		queryMethod = "sum"
	}
	return []execution.QueryFunction{{Method: queryMethod, Dimensions: append([]string(nil), dimensions...), Arguments: append([]execution.QueryScalar(nil), arguments...)}},
		execution.QueryFunction{Method: method + "_over_time", Window: strconv.FormatInt(window, 10) + "s", Position: position, Arguments: append([]execution.QueryScalar(nil), arguments...)}, nil
}

func compileConditions(source []legacyCondition) (execution.QueryConditions, error) {
	result := execution.QueryConditions{Fields: make([]execution.QueryConditionField, 0, len(source)), Connectors: make([]string, 0, max(0, len(source)-1))}
	operatorMapping := map[string]string{"reg": "req", "nreg": "nreq", "include": "req", "exclude": "nreq", "eq": "contains", "neq": "ncontains"}
	for index, condition := range source {
		if condition.Key == "" || condition.Method == "" {
			return execution.QueryConditions{}, errors.New("condition key and method are required")
		}
		values, err := pythonStringValues(condition.Value)
		if err != nil || len(values) == 0 {
			return execution.QueryConditions{}, errors.New("condition values must be scalar")
		}
		typed := make([]execution.QueryScalar, 0, len(values))
		for _, value := range values {
			typed = append(typed, execution.QueryScalar{Kind: execution.QueryScalarString, StringValue: value})
		}
		operator := condition.Method
		if mapped := operatorMapping[operator]; mapped != "" {
			operator = mapped
		}
		result.Fields = append(result.Fields, execution.QueryConditionField{Field: condition.Key, Operator: operator, Values: typed})
		if index > 0 {
			connector := condition.Connector
			if connector == "" {
				connector = "and"
			}
			result.Connectors = append(result.Connectors, connector)
		}
	}
	return result, nil
}

func compileMetricMerge(expression string, rawFunctions []json.RawMessage, queries []execution.QueryClause) (string, error) {
	if expression == "" {
		references := make([]string, 0, len(queries))
		for _, query := range queries {
			references = append(references, query.ReferenceName)
		}
		expression = strings.Join(references, " or ")
	}
	result := strings.ToLower(expression)
	for _, raw := range rawFunctions {
		function, err := decodeLegacyFunction(raw)
		if err != nil {
			return "", queryConfigRejected("EXPRESSION_FUNCTION_INVALID", err)
		}
		positions := map[string]int{"topk": 1, "bottomk": 1, "abs": 0, "ceil": 0, "floor": 0, "round": 0, "ln": 0, "log2": 0, "log10": 0, "sgn": 0, "sqrt": 0}
		position, supported := positions[function.ID]
		if !supported {
			return "", queryUnsupported("EXPRESSION_FUNCTION_NOT_MIGRATED", fmt.Errorf("function %q", function.ID))
		}
		params := make([]string, 0, len(function.Params))
		for _, param := range function.Params {
			value, err := pythonStringScalar(param.Value)
			if err != nil {
				return "", queryConfigRejected("EXPRESSION_FUNCTION_INVALID", err)
			}
			params = append(params, value)
		}
		if len(params) == 0 {
			result = function.ID + "(" + result + ")"
		} else if position == 0 {
			result = function.ID + "(" + result + "," + strings.Join(params, ",") + ")"
		} else {
			result = function.ID + "(" + strings.Join(params, ",") + "," + result + ")"
		}
	}
	if result == "" {
		return "", queryConfigRejected("METRIC_MERGE_EMPTY", nil)
	}
	return result, nil
}

type queryFunctionSpec struct {
	position        int32
	timeAggregation bool
	subquery        bool
	params          []queryFunctionParamSpec
}

type queryFunctionParamSpec struct {
	id   string
	kind execution.QueryScalarKind
}

var legacyQueryFunctionSpecs = map[string]queryFunctionSpec{
	"rate":               {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"irate":              {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"increase":           {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"deriv":              {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"delta":              {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"idelta":             {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"changes":            {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"resets":             {timeAggregation: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}}},
	"topk":               {position: 1, params: []queryFunctionParamSpec{{id: "k", kind: execution.QueryScalarNumber}}},
	"bottomk":            {position: 1, params: []queryFunctionParamSpec{{id: "k", kind: execution.QueryScalarNumber}}},
	"abs":                {},
	"ceil":               {},
	"floor":              {},
	"round":              {},
	"ln":                 {},
	"log2":               {},
	"log10":              {},
	"sgn":                {},
	"sqrt":               {},
	"histogram_quantile": {position: 1, params: []queryFunctionParamSpec{{id: "scalar", kind: execution.QueryScalarNumber}}},
	"sum_over_time":      {subquery: true, params: []queryFunctionParamSpec{{id: "window", kind: execution.QueryScalarString}, {id: "step", kind: execution.QueryScalarString}}},
}

func compileQueryFunctions(source []legacyFunction, baseTime execution.QueryFunction, dimensions []string) (execution.QueryFunction, []execution.QueryFunction, error) {
	result := make([]execution.QueryFunction, 0, len(source))
	seenTime := false
	for _, function := range source {
		spec, supported := legacyQueryFunctionSpecs[function.ID]
		if !supported || function.ID == "top" || function.ID == "bottom" {
			return execution.QueryFunction{}, nil, queryUnsupported("QUERY_FUNCTION_NOT_MIGRATED", fmt.Errorf("function %q", function.ID))
		}
		values := make(map[string]json.RawMessage, len(function.Params))
		for _, param := range function.Params {
			values[param.ID] = param.Value
		}
		compiled := execution.QueryFunction{Method: function.ID, Position: spec.position}
		for _, param := range spec.params {
			raw, found := values[param.id]
			if !found {
				return execution.QueryFunction{}, nil, queryConfigRejected("QUERY_FUNCTION_PARAM_REQUIRED", fmt.Errorf("%s.%s", function.ID, param.id))
			}
			scalar, err := typedFunctionScalar(raw, param.kind)
			if err != nil {
				return execution.QueryFunction{}, nil, queryConfigRejected("QUERY_FUNCTION_PARAM_INVALID", err)
			}
			switch param.id {
			case "window":
				compiled.Window = scalar.StringValue
			case "step":
				compiled.Step = scalar.StringValue
			default:
				compiled.Arguments = append(compiled.Arguments, scalar)
			}
		}
		if spec.subquery {
			compiled.Subquery = true
			result = append(result, compiled)
			continue
		}
		if spec.timeAggregation {
			if seenTime {
				return execution.QueryFunction{}, nil, queryConfigRejected("MULTIPLE_TIME_AGGREGATION_FUNCTIONS", nil)
			}
			seenTime = true
			baseTime.Method = compiled.Method
			baseTime.Window = compiled.Window
			baseTime.Position = compiled.Position
			baseTime.Arguments = compiled.Arguments
			continue
		}
		if function.ID == "histogram_quantile" && !containsString(dimensions, "le") {
			return execution.QueryFunction{}, nil, queryConfigRejected("QUERY_FUNCTION_PARAM_REQUIRED", errors.New("histogram_quantile.le"))
		}
		result = append(result, compiled)
	}
	return baseTime, result, nil
}

func extractTimeShift(functions *[]legacyFunction) (string, bool, error) {
	kept := make([]legacyFunction, 0, len(*functions))
	value := ""
	for _, function := range *functions {
		if function.ID != "time_shift" {
			kept = append(kept, function)
			continue
		}
		if len(function.Params) > 0 {
			parsed, err := pythonStringScalar(function.Params[0].Value)
			if err != nil {
				return "", false, err
			}
			value = parsed
		}
	}
	*functions = kept
	if value == "" {
		return "", false, nil
	}
	match := timeShiftPattern.FindStringSubmatch(value)
	if match == nil {
		return "", false, errors.New("unsupported time shift abbreviation")
	}
	amount, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return "", false, err
	}
	multipliers := map[string]int64{"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800}
	seconds := int64(amount * float64(multipliers[match[3]]))
	if seconds <= 0 {
		return "", false, errors.New("time shift must resolve to at least one second")
	}
	// Python parse_time_compare_abbreviation negates the configured offset:
	// 1h queries the past (offset_forward=false), while -1h moves forward.
	forward := match[1] == "-"
	return strconv.FormatInt(seconds, 10) + "s", forward, nil
}

func decodeLegacyFunction(raw json.RawMessage) (legacyFunction, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var function legacyFunction
	if err := decoder.Decode(&function); err != nil {
		return function, err
	}
	if function.ID == "" {
		return function, errors.New("function id is required")
	}
	return function, nil
}

func typedFunctionScalar(raw json.RawMessage, kind execution.QueryScalarKind) (execution.QueryScalar, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return execution.QueryScalar{}, err
	}
	switch kind {
	case execution.QueryScalarString:
		text, ok := value.(string)
		if !ok || text == "" {
			return execution.QueryScalar{}, errors.New("non-empty string function parameter is required")
		}
		return execution.QueryScalar{Kind: kind, StringValue: text}, nil
	case execution.QueryScalarNumber:
		var number string
		switch typed := value.(type) {
		case json.Number:
			number = typed.String()
		case string:
			number = typed
		default:
			return execution.QueryScalar{}, errors.New("numeric function parameter is required")
		}
		parsed, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return execution.QueryScalar{}, err
		}
		number = strconv.FormatFloat(parsed, 'g', -1, 64)
		return execution.QueryScalar{Kind: kind, NumberValue: number}, nil
	default:
		return execution.QueryScalar{}, errors.New("unsupported function parameter kind")
	}
}

func buildLegacyUQNormalization(identityFields []string) (execution.DatasetNormalizationSpec, error) {
	schemaFacts := struct {
		IdentityFields    []string `json:"identity_fields"`
		SourceTimeField   string   `json:"source_time_field"`
		ReceivedTimeField string   `json:"received_time_field"`
		ValueField        string   `json:"value_field"`
	}{append([]string{}, identityFields...), "_time", "_received_time", "value"}
	schemaDigest, err := contract.DeriveCanonicalDigestV2("alarmd-uq-dataset-schema-v1", schemaFacts)
	if err != nil {
		return execution.DatasetNormalizationSpec{}, err
	}
	normalizationFacts := struct {
		SourceTimeUnit          execution.TimeUnit           `json:"source_time_unit"`
		CanonicalSourceTimeUnit execution.TimeUnit           `json:"canonical_source_time_unit"`
		SeriesIdentityMode      execution.SeriesIdentityMode `json:"series_identity_mode"`
		GroupKeyRule            execution.GroupKeyRule       `json:"group_key_rule"`
		ValueSelectionMode      execution.ValueSelectionMode `json:"value_selection_mode"`
		CanonicalValueField     string                       `json:"canonical_value_field"`
		ReceivedTimeMode        execution.ReceivedTimeMode   `json:"received_time_mode"`
		Version                 string                       `json:"version"`
	}{execution.TimeUnitMillisecond, execution.TimeUnitSecond, execution.SeriesIdentityUQGroupKeysValuesV1,
		execution.GroupKeyStripTableSuffixV1, execution.ValueSelectionResultOrFirstReferenceV1, "value",
		execution.ReceivedTimeProviderReceivedAt, legacyUQNormalizationVersion}
	normalizationDigest, err := contract.DeriveCanonicalDigestV2("alarmd-uq-dataset-normalization-v1", normalizationFacts)
	if err != nil {
		return execution.DatasetNormalizationSpec{}, err
	}
	return execution.DatasetNormalizationSpec{
		DatasetContract: contract.DatasetContractV2{SchemaDigest: schemaDigest, NormalizationDigest: normalizationDigest,
			IdentityFields: append([]string{}, identityFields...), SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
		SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
		SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
		ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
		ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: legacyUQNormalizationVersion,
	}, nil
}

func pythonStringValues(raw json.RawMessage) ([]string, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		items = []any{value}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, err := pythonStringValue(item)
		if err != nil {
			return nil, err
		}
		result = append(result, text)
	}
	return result, nil
}

func pythonStringScalar(raw json.RawMessage) (string, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	return pythonStringValue(value)
}

func pythonStringValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "None", nil
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case bool:
		if typed {
			return "True", nil
		}
		return "False", nil
	default:
		return "", errors.New("nested query value is unsupported")
	}
}

func canonicalDimensionStrings(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func queryConfigRejected(reason string, err error) error {
	return &QueryPlanCompileError{Disposition: DispositionConfigRejected, Reason: reason, Err: err}
}

func queryUnsupported(reason string, err error) error {
	return &QueryPlanCompileError{Disposition: DispositionUnsupported, Reason: reason, Err: err}
}

func querySourceIncomplete(reason string, err error) error {
	return &QueryPlanCompileError{Disposition: DispositionSourceIncomplete, Reason: reason, Err: err}
}

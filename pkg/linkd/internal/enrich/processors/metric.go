// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// Metric 丰富告警关联的指标信息。
type Metric struct{}

// Name 返回稳定的 Processor 名称。
func (Metric) Name() string { return rules.MetricProcessor }

// Match 判断当前告警是否适用 Metric。
func (Metric) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 使用鲸眼声明式策略查询投影生成指标信息。
func (Metric) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Facts()
	values := models.MetricValues{}
	status := domain.EnrichStatusSucceeded
	diagnostics := make([]enrich.Diagnostic, 0, 1)
	if anomalyBeginTime, exists, valid := parseAnomalyBeginTime(alert.ExtraData); exists {
		if valid {
			values.AnomalyBeginTime = &anomalyBeginTime
		} else {
			status = domain.EnrichStatusPartial
			diagnostics = append(diagnostics, enrich.Diagnostic{
				Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data." + rules.FieldAnomalyBeginTime},
			})
		}
	}

	_, idDiagnostics := enrich.ValidateStrategyIdentity(alert)
	if len(idDiagnostics) != 0 {
		value, err := metricResultValue(scope, values)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return enrich.ProcessorResult{
			Status: domain.EnrichStatusFailed, Value: value,
			Diagnostics: append(diagnostics, idDiagnostics...),
		}, nil
	}
	strategy, strategyFound, strategyErr := scope.CWStrategy(ctx)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if strategyErr != nil || !strategyFound {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyKingeyeStrategy)
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyKingeyeStrategy)
	}
	strategy, projection, err = resolveMetricQueryLocations(ctx, scope, strategy, projection)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if err != nil {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyMetricCatalog)
	}
	query := projection.QueryConfigs[0]
	metricName := cleanMetricName(strategy, projection, query)
	unit, err := cleanUnit(ctx, scope, strategy, query)
	if err != nil {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyMetricCatalog)
	}
	aggregateFunc := cleanAggregateFunc(strategy)
	timeInterval := cleanTimeInterval(strategy)
	queryDimensions := metricDimensions(scope.Facts().Dimensions)
	displayName, err := cleanItem(ctx, scope, strategy, projection, query)
	if err != nil {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyMetricCatalog)
	}
	whereCondition := cleanWhereCondition(queryDimensions)
	resultTableID, metricField := metricIdentity(query)
	uniqueID := metricUniqueID(resultTableID, metricField)
	if strategy.Kind != models.CWStrategyKindCloud && strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
		if strategy.FirstExecutionQuery == nil {
			return enrich.ProcessorResult{}, fmt.Errorf("%w: kingeye_strategy items[0].query_configs[0] is missing or invalid", enrich.ErrInvalidDataSourceResponse)
		}
		table, field := metricIdentity(*strategy.FirstExecutionQuery)
		if field == "" {
			return enrich.ProcessorResult{}, fmt.Errorf("%w: kingeye_strategy first execution query has no identifiable metric field", enrich.ErrInvalidDataSourceResponse)
		}
		// 沿用 KAC develop/4.5.16 的首查询身份，包括空表名产生的前导点。
		// 这是兼容输出，不能据此改写衍生父指标声明或 metric_query_params。
		uniqueID = table + "." + field
	}
	values.DisplayName = displayName
	values.MetricName = metricName
	values.Unit = unit
	values.ResultTableID = resultTableID
	values.MetricUniqueID = uniqueID
	values.AggregateFunc = aggregateFunc
	values.TimeInterval = timeInterval
	values.WhereCondition = whereCondition
	// 所有来源均由消费侧组织指标查询；历史已存参数继续由解码和 Hook 投影承接。
	value, encodeErr := metricResultValue(scope, values)
	if encodeErr != nil {
		return enrich.ProcessorResult{}, encodeErr
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: diagnostics}, nil
}

// metricIdentity 返回冻结指标身份使用的结果表与字段。
// PromQL 查询的结果表可能来自冒号段规则，不再直接使用空的发布表名。
func metricIdentity(query models.StrategyQueryConfig) (string, string) {
	if query.IsPromQL() {
		return promQLMetricIdentity(query)
	}
	return query.ResultTableID, query.MetricField
}

// metricUniqueID 仅在结果表和字段都可确定时生成可反解的指标标识。
// 残缺拼接会得到 ".usage" 或 "table." 这类无法反解的值。
func metricUniqueID(table, field string) string {
	if table == "" || field == "" {
		return ""
	}
	return table + "." + field
}

// 指标目录策略只冻结目录身份，resolved 策略中的物理表可以为空或过期。
// 显式目录引用必须按租户精确读取主引用；不修改发布材料，也不猜测表名。
func resolveMetricQueryLocations(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, projection models.StrategyItemProjection) (models.CWStrategy, models.StrategyItemProjection, error) {
	// 显式目录引用仍须校验当前目录类型；旧式衍生声明没有引用时沿用原路径。
	if strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
		referenced := false
		for _, query := range projection.QueryConfigs {
			referenced = referenced || query.MetricRefID > 0
		}
		if !referenced {
			return strategy, projection, nil
		}
	}
	modelCode := ""
	if strategy.ObjectModelCode != nil {
		modelCode = *strategy.ObjectModelCode
	}
	for i := range projection.QueryConfigs {
		query := &projection.QueryConfigs[i]
		if query.MetricRefID == 0 {
			continue
		}
		metadata, found, err := metricDefinitionMetadata(ctx, scope, strategy, *query, modelCode)
		if err != nil {
			return strategy, projection, err
		}
		if strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric && metadata.Kind != "derived" {
			return strategy, projection, fmt.Errorf("%w: derived metric catalog kind mismatch", enrich.ErrInvalidDataSourceResponse)
		}
		if found && metadata.Kind == "derived" && metadata.RecordRuleID == nil && metadata.FieldName != "" {
			// 展开计算的目录定义没有物理主引用，不能把检测面成员当作父指标位置。
			// 物化型由 record_rule_id 区分，必须保留它自己的结果表与字段。
			if metadata.TableID != "" || metadata.DataLabel != "" || metadata.PhysicalField != "" {
				return strategy, projection, fmt.Errorf("%w: inline derived metric unexpectedly has a physical reference", enrich.ErrInvalidDataSourceResponse)
			}
			query.MetricField, query.ResultTableID = metadata.FieldName, ""
			if len(projection.QueryConfigs) == 1 {
				strategy.Spec.FieldTag, strategy.Spec.FieldName = models.CWStrategyFieldTagDerivedMetric, metadata.FieldName
				// catalog 的单别名只是引用已定义的衍生指标；实际表达式仍由目录定义负责。
				// 计算和函数必须保留，不能把未知表达式悄悄变成直接取数。
				if query.QueryConfigType == "catalog" && query.Alias != "" && strings.TrimSpace(projection.Expression) == query.Alias && !hasFunctions(projection.Functions) && !hasFunctions(query.Functions) {
					projection.Expression = ""
				}
			}
			continue
		}
		physicalKind := metadata.Kind == "native" || metadata.Kind == "predefined" ||
			(metadata.Kind == "derived" && metadata.RecordRuleID != nil && *metadata.RecordRuleID > 0)
		if !found || !physicalKind || metadata.PhysicalField == "" || (metadata.TableID == "" && metadata.DataLabel == "") {
			return strategy, projection, fmt.Errorf("%w: metric catalog physical reference is incomplete or kind is invalid", enrich.ErrInvalidDataSourceResponse)
		}
		query.ResultTableID = metadata.TableID
		if query.ResultTableID == "" {
			query.ResultTableID = metadata.DataLabel
		}
		query.MetricField = metadata.PhysicalField
		if metadata.Kind == "derived" && len(projection.QueryConfigs) == 1 && strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
			strategy.Spec.FieldName = metadata.FieldName
		}
	}
	return strategy, projection, nil
}

// promQLSeriesPattern 同时识别普通指标和冒号分段指标，防止混合表达式遗漏序列。
var promQLSeriesPattern = regexp.MustCompile(`[A-Za-z_:][A-Za-z0-9_:]*`)

// parsePromQLIdentity 按 Kingeye 的冒号段规则从 PromQL 提取结果表与指标字段。
// 表达式必须能唯一确定一个冒号分段的序列名；函数、聚合、by/on 列表、标签匹配器
// 和时间窗口不参与命名。多序列或无法唯一确定时返回空表与空字段，避免把任意
// PromQL 强行绑定到单个指标。返回顺序为字段、结果表，与调用方投影一致。
func parsePromQLIdentity(promql string) (string, string) {
	series, ok := promQLSeriesName(promql)
	if !ok {
		return "", ""
	}
	parts := strings.Split(series, ":")
	if len(parts) < 3 {
		return "", ""
	}
	return parts[len(parts)-1], strings.Join(parts[1:len(parts)-1], ".")
}

// promQLSeriesName 返回表达式中唯一的冒号分段序列名。
// 字符串、标签匹配器和区间选择器先被移除，因此其中的冒号不会被误认成序列；
// 结构不完整（未闭合的引号、圆括号、花括号或方括号）时拒绝解析，避免基于残缺
// 输入猜测身份。
func promQLSeriesName(expression string) (string, bool) {
	stripped, ok := stripPromQLSeriesContext(expression)
	if !ok {
		return "", false
	}
	// 无名选择器和注释无法由这套有限识别规则证明身份，保守放弃目录推导。
	if strings.ContainsAny(stripped, "?#") {
		return "", false
	}
	result, skipUntil := "", 0
	for _, index := range promQLSeriesPattern.FindAllStringIndex(stripped, -1) {
		if index[0] < skipUntil {
			continue
		}
		// 数值和 offset 时间单位不是序列，例如 5m、1e3。
		if index[0] > 0 && stripped[index[0]-1] >= '0' && stripped[index[0]-1] <= '9' {
			continue
		}
		name := stripped[index[0]:index[1]]
		rest := strings.TrimLeft(stripped[index[1]:], " \t\r\n")
		switch name {
		case "by", "without", "on", "ignoring", "group_left", "group_right":
			if strings.HasPrefix(rest, "(") {
				// 这些括号只包含标签列表；函数括号中的指标则必须继续扫描。
				end := strings.IndexByte(rest, ')')
				if end < 0 {
					return "", false
				}
				skipUntil = len(stripped) - len(rest) + end + 1
			}
			continue
		case "sum", "min", "max", "avg", "group", "stddev", "stdvar", "count", "count_values", "bottomk", "topk", "quantile", "limitk", "limit_ratio":
			words := strings.Fields(rest)
			if len(words) != 0 && (words[0] == "by" || words[0] == "without" || strings.HasPrefix(words[0], "by(") || strings.HasPrefix(words[0], "without(")) {
				continue
			}
		case "offset", "bool", "and", "or", "unless":
			continue
		}
		if strings.HasPrefix(rest, "(") {
			continue
		}
		if result != "" && result != name {
			return "", false
		}
		result = name
	}
	if result == "" {
		return "", false
	}
	return result, true
}

// stripPromQLSeriesContext 移除字符串字面量、标签匹配器和区间选择器，
// 保留函数、聚合、运算符和裸标识符供后续识别。
func stripPromQLSeriesContext(expression string) (string, bool) {
	var stripped strings.Builder
	stripped.Grow(len(expression))
	braces, brackets, parens := 0, 0, 0
	var quote byte
	for i := 0; i < len(expression); i++ {
		char := expression[i]
		switch {
		case quote != 0:
			if char == '\\' && quote != '`' {
				i++
				continue
			}
			if char == quote {
				quote = 0
			}
		case char == '"' || char == '\'' || char == '`':
			quote = char
			stripped.WriteByte(' ')
		case char == '{':
			// 只有紧邻已有指标名的匹配器才能沿用其身份；{__name__=...}
			// 本身也是序列，不能在混合表达式中把它静默删除。
			if braces == 0 {
				prefix := strings.TrimRight(expression[:i], " \t\r\n")
				words := strings.Fields(prefix)
				if len(words) != 0 {
					switch words[len(words)-1] {
					case "and", "or", "unless", "bool":
						stripped.WriteByte('?')
					}
				}
				if prefix == "" || !strings.ContainsAny(prefix[len(prefix)-1:], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_:") {
					stripped.WriteByte('?')
				}
			}
			braces++
		case char == '}':
			if braces == 0 {
				return "", false
			}
			braces--
		case char == '[':
			brackets++
		case char == ']':
			if brackets == 0 {
				return "", false
			}
			brackets--
		case braces != 0 || brackets != 0:
			// 标签匹配器和区间选择器内部不产生序列名。
		case char == '(':
			parens++
			stripped.WriteByte(char)
		case char == ')':
			if parens == 0 {
				return "", false
			}
			parens--
			stripped.WriteByte(char)
		default:
			stripped.WriteByte(char)
		}
	}
	if quote != 0 || braces != 0 || brackets != 0 || parens != 0 {
		return "", false
	}
	return stripped.String(), true
}

// promQLMetricIdentity 返回 PromQL 查询的兼容指标身份。
// 发布材料已带结果表或字段时沿用发布值，缺失时才按冒号段规则从表达式派生。
func promQLMetricIdentity(query models.StrategyQueryConfig) (string, string) {
	table, field := query.ResultTableID, query.MetricField
	if table != "" && field != "" {
		return table, field
	}
	derivedField, derivedTable := parsePromQLIdentity(query.PromQL)
	if field == "" {
		field = derivedField
	}
	if table == "" {
		table = derivedTable
	}
	return table, field
}

func metricResultValue(scope *enrich.Scope, values models.MetricValues) (domain.JSONObject, error) {
	scope.Context().Metric.Set(values)
	return scope.Context().Metric.JSONObject()
}

func metricDependencyFailure(
	scope *enrich.Scope,
	values models.MetricValues,
	diagnostics []enrich.Diagnostic,
	dependency string,
) (enrich.ProcessorResult, error) {
	value, err := metricResultValue(scope, values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	return enrich.ProcessorResult{
		Status: domain.EnrichStatusFailed, Value: value,
		Diagnostics: append(diagnostics, enrich.Diagnostic{
			Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency,
		}),
	}, nil
}

func aggregatePeriodSeconds(raw json.RawMessage) int64 {
	var seconds int64
	if json.Unmarshal(raw, &seconds) == nil {
		return seconds
	}
	var text string
	if json.Unmarshal(raw, &text) != nil || !strings.HasSuffix(text, "s") {
		return 0
	}
	seconds, _ = strconv.ParseInt(strings.TrimSuffix(text, "s"), 10, 64)
	return seconds
}

func parseAnomalyBeginTime(extraData domain.JSONObject) (string, bool, bool) {
	raw, exists := extraData[rules.FieldAnomalyBeginTime]
	if !exists {
		return "", false, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false
	}
	return value, true, true
}

// 无数据的规范模型身份用于资源定位，不是指标序列的过滤条件；
// 普通事件仍保留原有查询维度，所有处理都在副本上完成。
func metricDimensions(dimensions domain.DimensionMap) domain.DimensionMap {
	if !rules.ScalarProvided(dimensions[rules.FieldNoDataDimension]) {
		return dimensions
	}
	result := dimensions.Clone()
	delete(result, rules.FieldModelID)
	delete(result, rules.FieldModelInstID)
	return result
}

func cleanWhereCondition(dimensions domain.DimensionMap) string {
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		if key == rules.FieldBKTopoNode || key == rules.FieldBKHostID || key == rules.FieldNoDataDimension {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	conditions := make([]string, 0, len(keys))
	for _, key := range keys {
		conditions = append(conditions, fmt.Sprintf("%s='%v'", key, rules.ScalarValue(dimensions[key])))
	}
	return strings.Join(conditions, " "+rules.ConditionAnd+" ")
}

func cleanAggregateFunc(strategy models.CWStrategy) string {
	if strategy.Spec.StrategyItem == nil {
		return rules.DefaultAggregateFunc
	}
	return strategy.Spec.StrategyItem.AggregateMethod
}

func cleanTimeInterval(strategy models.CWStrategy) any {
	if strategy.Spec.StrategyItem == nil {
		return rules.DefaultTimeInterval
	}
	if seconds := aggregatePeriodSeconds(strategy.Spec.StrategyItem.AggregatePeriod); seconds > 0 {
		return seconds
	}
	return rules.DefaultTimeInterval
}

func cleanUnit(
	ctx context.Context,
	scope *enrich.Scope,
	strategy models.CWStrategy,
	query models.StrategyQueryConfig,
) (string, error) {
	if strategy.Spec.StrategyItem == nil || strategy.Spec.MonitorItemType == models.CWMonitorItemTypeLogKeyword {
		// 日志关键字
		return "", nil
	}
	isDerivedMetric := strategy.Kind != models.CWStrategyKindCloud &&
		strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric
	modelCode := ""
	if strategy.ObjectModelCode != nil {
		modelCode = *strategy.ObjectModelCode
	}
	metadata, found, err := metricDefinitionMetadata(ctx, scope, strategy, query, modelCode)
	if err != nil {
		return "", err
	}
	if found && metadata.Unit != "" {
		return metadata.Unit, nil
	}
	if isDerivedMetric {
		// 指标库未命中，回落到 query_config 中的 unit 字段
		return query.Unit, nil
	}
	if strategy.ObjectModelCode != nil && *strategy.ObjectModelCode == rules.UptimeModelCode {
		// 拨测指标，固定返回
		return rules.DefaultUptimeUnit, nil
	}
	return "", nil
}

func cleanMetricName(
	strategy models.CWStrategy,
	projection models.StrategyItemProjection,
	query models.StrategyQueryConfig,
) string {
	if strategy.Spec.MonitorItemType == models.CWMonitorItemTypeLog ||
		strategy.Spec.MonitorItemType == models.CWMonitorItemTypeLogKeyword {
		if strategy.Spec.AliasName != "" {
			return strategy.Spec.AliasName
		}
		if queryString := sourceConfigString(strategy.Spec.SourceConfig, rules.FieldQueryString); queryString != "" {
			return queryString
		}
		// 日志指标和日志关键字
		return rules.LogMetricFallback
	}
	if strategy.Kind != models.CWStrategyKindCloud && strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
		// 衍生指标
		return strategy.Spec.FieldName
	}
	if query.IsPromQL() {
		// PromQL 不对应单指标：发布字段缺失时按冒号段规则派生，派生不出则留空。
		_, field := promQLMetricIdentity(query)
		return field
	}
	if strategy.Spec.StrategyItem == nil ||
		hasFunctions(strategy.Spec.StrategyItem.Functions) || hasFunctions(projection.Functions) {
		return ""
	}
	for _, member := range projection.QueryConfigs {
		if hasFunctions(member.Functions) {
			return ""
		}
	}
	// Projection 对应单个监控项；多条 query 是该项表达式的输入。
	// 沿用旧 KAC 的首查询字段作为兼容指标名，完整取数语义由
	// 消费侧的查询逻辑承接；Linkd 不再生成 metric_query_params。
	return query.MetricField
}

func sourceConfigString(sourceConfig domain.JSONObject, field string) string {
	raw, exists := sourceConfig[field]
	if !exists {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func hasFunctions(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null")) &&
		!bytes.Equal(trimmed, []byte("[]")) && !bytes.Equal(trimmed, []byte(`""`))
}

func cleanItem(
	ctx context.Context,
	scope *enrich.Scope,
	strategy models.CWStrategy,
	projection models.StrategyItemProjection,
	query models.StrategyQueryConfig,
) (string, error) {
	modelCode := ""
	if strategy.ObjectModelCode != nil {
		modelCode = *strategy.ObjectModelCode
	}
	if query.IsPromQL() {
		// PromQL 没有单指标名称：目录能定位时才用中文名补充，否则保留发布名称，
		// 不能因为目录缺数据而失去展示名称。
		metadata, found, err := metricDefinitionMetadata(ctx, scope, strategy, query, modelCode)
		if err != nil {
			return "", err
		}
		if found && metadata.FieldCNName != "" {
			return metadata.FieldCNName, nil
		}
		return strategy.Spec.Name, nil
	}
	if strategy.Spec.StrategyItem != nil && len(strategy.Spec.StrategyItem.QueryConfigs) != 0 || modelCode == "" {
		// 基于数据或多指标
		return strategy.Spec.Name, nil
	}
	tableID := query.ResultTableID
	if tableID == "" {
		tableID = strategy.Spec.TableID
	}
	if tableID == rules.SystemEventTableID {
		// 系统事件
		return projection.Name, nil
	}
	metadata, found, err := metricDefinitionMetadata(ctx, scope, strategy, query, modelCode)
	if err != nil {
		return "", err
	}
	if found && metadata.FieldCNName != "" {
		return metadata.FieldCNName, nil
	}
	// 未命中指标目录时沿用发布材料的 metric_field。
	return query.MetricField, nil
}

// 日志主题查询可能没有目录引用；此时展示沿用日志配置与原始维度，不能把缺少
// 物理表当成目录故障。其他指标仍要求完整身份；日志显式引用的目录失败也不掩盖。
// PromQL 表达式无法唯一确定单指标时同样没有目录引用，目录只作可选补充。
func metricDefinitionMetadata(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, query models.StrategyQueryConfig, modelCode string) (models.MetricMetadata, bool, error) {
	locator := metricDefinitionQuery(strategy, query, modelCode)
	if rules.IsLogDisplay(rules.ClassifyDisplay(strategy)) && locator.MetricID == 0 && locator.TableID == "" && locator.FieldTag != models.CWStrategyFieldTagDerivedMetric {
		return models.MetricMetadata{}, false, nil
	}
	if query.IsPromQL() && !locator.LocatorComplete() {
		return models.MetricMetadata{}, false, nil
	}
	if query.IsPromQL() && locator.MetricID == 0 {
		return scope.OptionalMetricMetadata(ctx, locator)
	}
	return scope.MetricMetadata(ctx, locator)
}

// 定义查询统一从发布材料提取，避免单位、文案、维度和资源分别定位到不同的指标行。
// PromQL 声明常以冒号段表达单一序列，缺少表名或字段时按同一规则派生目录定位，
// 派生失败只说明目录不可补充，不能据此判定丰富失败。
func metricDefinitionQuery(strategy models.CWStrategy, query models.StrategyQueryConfig, modelCode string) models.MetricQuery {
	result := models.MetricQuery{MetricID: query.MetricRefID, TableID: query.ResultTableID, FieldName: query.MetricField, ObjectModelCode: modelCode}
	if result.TableID == "" {
		result.TableID = strategy.Spec.TableID
	}
	if query.IsPromQL() {
		result.TableID, result.FieldName = promQLMetricIdentity(query)
	}
	if strategy.Kind != models.CWStrategyKindCloud && strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
		result.TableID = ""
		result.FieldName = strategy.Spec.FieldName
		result.FieldTag = models.CWStrategyFieldTagDerivedMetric
	}
	return result
}

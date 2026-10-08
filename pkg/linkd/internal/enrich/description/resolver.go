// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package description

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

// Resolver 按事件身份取得已验证发布配置，再整理检测描述输入。
// 无法从 standard Event 证明的历史/预测事实明确失败，不解析来源 content。
type Resolver struct{ configurations ConfigurationReader }

var _ FactsResolver = (*Resolver)(nil)

// NewResolver 创建生产事实解析器；真实配置读取是必需依赖。
func NewResolver(reader ConfigurationReader) (*Resolver, error) {
	if reader == nil {
		return nil, fmt.Errorf("description configuration reader is required")
	}
	return &Resolver{configurations: reader}, nil
}

// Resolve 只使用当前 opening Event 的选中 triggered 判定及精确版本配置。
// 数值原始 JSON 保留整数/浮点的格式语义；缺值不能当作零。
func (r *Resolver) Resolve(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (Facts, error) {
	if ctx == nil {
		return Facts{}, invalid("context_missing")
	}
	if err := ctx.Err(); err != nil {
		return Facts{}, err
	}
	if r == nil || r.configurations == nil {
		return Facts{}, invalid("configuration_reader_missing")
	}
	if event.BKTenantID != opening.BKTenantID || event.EventSourceID != opening.EventSourceID || event.EventSourceVersion != opening.EventSourceVersion || evaluation.Severity != opening.Severity || evaluation.Action != domain.EventActionTriggered {
		return Facts{}, invalid("opening_identity_invalid")
	}
	readInt := func(name string) (int64, error) {
		value, ok := event.Labels[name].NumberValue()
		if !ok || value == 0 || math.Abs(value) >= 1<<53 || math.Trunc(value) != value || (name != "bk_biz_id" && value < 0) {
			return 0, invalid("strategy_label_invalid")
		}
		return int64(value), nil
	}
	strategy, err := readInt("strategy_id")
	if err != nil {
		return Facts{}, err
	}
	version, err := readInt("strategy_version")
	if err != nil {
		return Facts{}, err
	}
	business, err := readInt("bk_biz_id")
	if err != nil {
		return Facts{}, err
	}
	identity := ConfigurationQuery{TenantID: event.BKTenantID, StrategyID: strategy, StrategyVersion: version, BusinessID: business}
	configuration, err := r.configurations.ReadConfiguration(ctx, identity)
	if err != nil {
		return Facts{}, fmt.Errorf("read description configuration: %w", err)
	}
	if configuration.Identity != identity {
		return Facts{}, invalid("configuration_identity_mismatch")
	}
	// 发布投影器会在编译以后重写专用指标的表达式。Ping 的描述只依赖
	// 原始丢包率，且 EventExecutor 已冻结 PingUnreachable 算法；仅接受这一
	// 明确组合。其他专用指标不能当作普通阈值处理。
	for _, query := range configuration.Queries {
		switch query.MetricID {
		case "bk_monitor.ping-gse":
			level := map[string]int64{"critical": 1, "warning": 2, "info": 3}[evaluation.Severity]
			selected := 0
			for _, algorithm := range configuration.Algorithms {
				if algorithm.Level != level {
					continue
				}
				if algorithm.Type != "PingUnreachable" {
					return Facts{}, invalid("special_query_rewrite_unverified")
				}
				selected++
			}
			if len(configuration.Queries) != 1 || selected != 1 {
				return Facts{}, invalid("special_query_rewrite_unverified")
			}
		case "bk_monitor.os_restart", "bk_monitor.proc_port":
			return Facts{}, invalid("special_query_rewrite_unverified")
		}
	}
	if err := ctx.Err(); err != nil {
		return Facts{}, err
	}
	if _, nodata := event.Dimensions["__NO_DATA_DIMENSION__"]; nodata {
		return Facts{}, invalid("nodata_evidence_missing")
	}
	var family string
	if raw, ok := event.ExtraData["evaluation_family"]; ok && (json.Unmarshal(raw, &family) != nil || family != "metric_algorithm") {
		return Facts{}, invalid("detection_evidence_missing")
	}
	value, err := eventObservedNumber(event)
	if err != nil {
		return Facts{}, err
	}
	name, err := configurationItemName(configuration.Spec)
	if err != nil {
		return Facts{}, err
	}
	unit, err := configurationUnit(configuration.Queries)
	if err != nil {
		return Facts{}, err
	}
	// 生产 Converter 的非空 observed.unit 必须与发布查询的单位一致。
	// 不进行猜测换算：值是 record.Values 的快照，不是 detector 的归一化中间值。
	if raw, ok := event.ExtraData["unit"]; ok {
		var observedUnit string
		if json.Unmarshal(raw, &observedUnit) != nil || (observedUnit != "" && observedUnit != unit) {
			return Facts{}, invalid("observed_unit_mismatch")
		}
	}
	level := map[string]int64{"critical": 1, "warning": 2, "info": 3}[evaluation.Severity]
	if level == 0 {
		return Facts{}, invalid("severity_mapping_missing")
	}
	item := configuration.Spec.StrategyItem
	connector := strings.ToLower(item.Connector)
	if connector == "" {
		connector = "and"
	}
	facts := Facts{ItemName: name, Unit: unit, Value: value, Connector: connector}
	if len(configuration.Algorithms) > maxAlgorithms {
		return Facts{}, invalid("algorithm_limit")
	}
	for _, compiled := range configuration.Algorithms {
		if compiled.Level != level {
			continue
		}
		algorithm := Algorithm{Type: compiled.Type, UnitPrefix: compiled.UnitPrefix}
		switch compiled.Type {
		case "Threshold":
			groups, err := configurationThreshold(compiled.Config)
			if err != nil {
				return Facts{}, err
			}
			algorithm.Groups = groups
		case "PingUnreachable":
			// 此规则仅比较当次原始值 >= 1，不需要重查历史。
		case "SimpleRingRatio", "SimpleYearRound", "AdvancedRingRatio", "AdvancedYearRound", "RingRatioAmplitude", "TimeSeriesForecasting", "OsRestart":
			return Facts{}, invalid("detection_evidence_missing")
		case "ProcPort":
			return Facts{}, invalid("process_evidence_missing")
		default:
			return Facts{}, invalid("algorithm_unsupported")
		}
		facts.Algorithms = append(facts.Algorithms, algorithm)
	}
	if len(facts.Algorithms) == 0 {
		return Facts{}, invalid("selected_level_missing")
	}
	return facts, nil
}

// 普通 Data/Target/Log/APM 继承 Kingeye BaseExecutor.build_items；PromQL
// 在 DataExecutor 中覆盖 name，EventExecutor 使用 spec.name。对象名不参与前缀。
func configurationItemName(spec models.CWStrategySpec) (string, error) {
	item := spec.StrategyItem
	if item == nil || len(item.QueryConfigs) > 32 {
		return "", invalid("strategy_item_invalid")
	}
	// EventExecutor 根据 field_name 合成查询，声明的 query_configs 可以为空。
	if spec.MonitorItemType == "event" && spec.Name != "" {
		return spec.Name, nil
	}
	if len(item.QueryConfigs) == 0 {
		return "", invalid("strategy_item_invalid")
	}
	if item.QueryConfigs[0].PromQL != "" {
		return item.QueryConfigs[0].PromQL, nil
	}
	if spec.Name == "" {
		return "", invalid("item_name_missing")
	}
	if item.AggregateMethod == "" {
		return "", invalid("aggregate_method_missing")
	}
	return item.AggregateMethod + "(" + spec.Name + ")", nil
}

// Standard Cleaner 的数值视图使用 float64，整数/浮点词法只能从同一原始
// payload 的 values 读取。规范化规则修改过值时拒绝混用两份不同的事实。
// 这里保留的是接入线上的数值类型，不保证上游转换前的类型仍然存在。
func eventObservedNumber(event domain.Event) (Number, error) {
	observed, ok := event.Values["value"]
	if !ok {
		return Number{}, invalid("observed_value_missing")
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(event.SourceRawData["values"], &values) != nil {
		return Number{}, invalid("observed_number_type_missing")
	}
	value, err := ParseNumber(values["value"])
	if err != nil {
		return Number{}, err
	}
	if value.value != observed {
		return Number{}, invalid("observed_value_mismatch")
	}
	return value, nil
}

func configurationUnit(queries []models.StrategyQueryConfig) (string, error) {
	if len(queries) == 0 || len(queries) > 32 {
		return "", invalid("query_configuration_invalid")
	}
	unit := ""
	for _, query := range queries {
		if query.Unit == "" {
			continue
		}
		if unit != "" && unit != query.Unit {
			return "", invalid("query_unit_ambiguous")
		}
		unit = query.Unit
	}
	return unit, nil
}

func configurationThreshold(raw json.RawMessage) ([][]Condition, error) {
	var groups [][]struct {
		Method string          `json:"method"`
		Value  json.RawMessage `json:"threshold"`
	}
	if json.Unmarshal(raw, &groups) != nil || len(groups) == 0 || len(groups) > maxGroups {
		return nil, invalid("threshold_config_invalid")
	}
	result := make([][]Condition, len(groups))
	for index, group := range groups {
		if len(group) == 0 || len(group) > maxConditions {
			return nil, invalid("threshold_config_invalid")
		}
		for _, condition := range group {
			if strings.TrimSpace(string(condition.Value)) == "null" {
				return nil, invalid("threshold_config_invalid")
			}
			var value float64
			decodeErr := json.Unmarshal(condition.Value, &value)
			if decodeErr != nil {
				var text string
				if len(condition.Value) > 128 || json.Unmarshal(condition.Value, &text) != nil {
					return nil, invalid("threshold_config_invalid")
				}
				value, decodeErr = strconv.ParseFloat(text, 64)
			}
			if decodeErr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, invalid("threshold_config_invalid")
			}
			result[index] = append(result[index], Condition{Method: condition.Method, Threshold: value})
		}
	}
	return result, nil
}

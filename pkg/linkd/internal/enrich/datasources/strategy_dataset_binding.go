package datasources

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"

	"linkd/internal/enrich/models"
)

// bindRevisionDatasetLocations 只连接同一不可变修订中的 dataset 声明与物理查询。
// 语义身份由唯一别名、数据集和 dataset_semantic 字段共同证明；声明的表达式、
// 函数及过滤条件仍由声明负责。目前只支持维度、条件无需改名的直接映射，
// 无法证明等价时拒绝丰富，不能把编译展开的首个查询当成原声明。
func bindRevisionDatasetLocations(spec *models.CWStrategySpec, items []json.RawMessage) error {
	if spec.StrategyItem == nil {
		return nil
	}
	indices := []int{}
	for i, query := range spec.StrategyItem.QueryConfigs {
		if query.QueryConfigType == "dataset" {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return nil
	}
	invalid := func() error { return descriptionFailure("strategy_revision_dataset_binding_invalid") }
	queries := spec.StrategyItem.QueryConfigs
	if len(queries) > 32 || len(items) != 1 {
		return invalid()
	}
	var runtime struct {
		Error   json.RawMessage `json:"error"`
		Queries []struct {
			Alias           string          `json:"alias"`
			DatasetCode     string          `json:"dataset_code"`
			MetricField     string          `json:"metric_field"`
			ResultTableID   string          `json:"result_table_id"`
			DataSourceLabel string          `json:"data_source_label"`
			DataTypeLabel   string          `json:"data_type_label"`
			PromQL          string          `json:"promql"`
			AggregateBy     []string        `json:"agg_dimension"`
			AggregateFilter json.RawMessage `json:"agg_condition"`
			Semantic        struct {
				Field         string   `json:"metric_field"`
				Dimensions    []string `json:"agg_dimension"`
				ConditionKeys []string `json:"agg_condition_keys"`
			} `json:"dataset_semantic"`
			FilterDict    map[string]any `json:"filter_dict"`
			Unbound       bool           `json:"unbound"`
			InvalidSource bool           `json:"invalid_source"`
		} `json:"query_configs"`
	}
	if json.Unmarshal(items[0], &runtime) != nil || len(runtime.Error) != 0 || len(runtime.Queries) != len(queries) {
		return invalid()
	}
	projection, err := (models.CWStrategy{Spec: *spec}).StrategyItemProjection()
	if err != nil {
		return invalid()
	}
	// 先完成全部校验，再改写读取视图；Raw 和原始快照字节不变。
	locations := make(map[int]models.StrategyQueryConfig, len(indices))
	for _, index := range indices {
		query := projection.QueryConfigs[index]
		aliases := 0
		for _, declared := range queries {
			if declared.Alias == query.Alias {
				aliases++
			}
		}
		if aliases != 1 || query.Alias == "" || strings.TrimSpace(query.DatasetCode) == "" || query.MetricField == "" || query.MetricRefID != 0 || query.IsPromQL() {
			return invalid()
		}
		matches := 0
		for _, compiled := range runtime.Queries {
			if compiled.Alias != query.Alias {
				continue
			}
			matches++
			if compiled.DatasetCode != query.DatasetCode || compiled.Semantic.Field != query.MetricField ||
				compiled.Unbound || compiled.InvalidSource || strings.TrimSpace(compiled.ResultTableID) == "" || strings.TrimSpace(compiled.MetricField) == "" ||
				compiled.DataSourceLabel != "bk_monitor" || compiled.DataTypeLabel != "time_series" || compiled.PromQL != "" || len(compiled.FilterDict) != 0 ||
				!slices.Equal(compiled.Semantic.Dimensions, query.AggregateBy) || !slices.Equal(compiled.AggregateBy, query.AggregateBy) ||
				!datasetConditionsMatch(query.AggregateFilter, compiled.AggregateFilter, compiled.Semantic.ConditionKeys) {
				return invalid()
			}
			locations[index] = models.StrategyQueryConfig{MetricField: compiled.MetricField, ResultTableID: compiled.ResultTableID, DataSourceLabel: compiled.DataSourceLabel, DataTypeLabel: compiled.DataTypeLabel}
		}
		if matches != 1 {
			return invalid()
		}
	}
	for index, location := range locations {
		queries[index].MetricField = location.MetricField
		queries[index].ResultTableID = location.ResultTableID
		queries[index].DataSourceLabel = location.DataSourceLabel
		queries[index].DataTypeLabel = location.DataTypeLabel
	}
	return nil
}

func datasetConditionsMatch(declared, compiled json.RawMessage, semanticKeys []string) bool {
	var left, right []map[string]any
	// 条件中的大整数不能先转 float64，否则不同的主机/实例 ID 可能被误判为相同。
	decode := func(raw json.RawMessage, output any) error {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		return decoder.Decode(output)
	}
	if decode(declared, &left) != nil || decode(compiled, &right) != nil || !slices.EqualFunc(left, right, func(a, b map[string]any) bool { return reflect.DeepEqual(a, b) }) {
		return false
	}
	keys := make(map[string]bool, len(left))
	for _, condition := range left {
		key, ok := condition["key"].(string)
		if !ok || key == "" {
			return false
		}
		keys[key] = true
	}
	boundKeys := make(map[string]bool, len(semanticKeys))
	for _, key := range semanticKeys {
		boundKeys[key] = true
	}
	return maps.Equal(keys, boundKeys)
}

package datasources

import (
	"encoding/json"

	"linkd/internal/enrich/models"
)

// bindRevisionMetricReferences 仅在执行快照携带目录引用时补齐声明身份；物理查询
// 和日志索引集不因此被强制绑定目录。实际补齐仍要求唯一别名及相同物理字段，
// 不把编译后的表达式、条件或展开成员覆盖回声明。
func bindRevisionMetricReferences(spec *models.CWStrategySpec, runtimeItems []json.RawMessage) error {
	if spec.StrategyItem == nil || spec.FieldTag == models.CWStrategyFieldTagDerivedMetric ||
		spec.MetricSource == models.CWMetricSourceKLC ||
		spec.MonitorItemType == models.CWMonitorItemTypeLog || spec.MonitorItemType == models.CWMonitorItemTypeLogKeyword {
		return nil
	}
	queries := spec.StrategyItem.QueryConfigs
	missing := make([]int, 0, len(queries))
	for i, query := range queries {
		if query.MetricRefID == 0 && query.ResultTableID == "" && spec.TableID == "" && query.PromQL == "" {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if len(queries) > 32 || len(runtimeItems) != 1 {
		return descriptionFailure("strategy_revision_metric_binding_invalid")
	}
	// 只读取绑定所需身份；执行 DTO 的其他字段不参与声明查询的目录校验。
	var runtime struct {
		Error   json.RawMessage `json:"error"`
		Queries []struct {
			Alias       string `json:"alias"`
			MetricField string `json:"metric_field"`
			MetricRefID int64  `json:"metric_ref_id"`
		} `json:"query_configs"`
	}
	if json.Unmarshal(runtimeItems[0], &runtime) != nil || len(runtime.Error) != 0 || len(runtime.Queries) != len(queries) {
		return descriptionFailure("strategy_revision_metric_binding_invalid")
	}
	hasReference := false
	for _, query := range runtime.Queries {
		if query.MetricRefID < 0 {
			return descriptionFailure("strategy_revision_metric_binding_invalid")
		}
		hasReference = hasReference || query.MetricRefID > 0
	}
	// 没有目录引用时，编译可以改变字段或别名；不能把未采用目录的查询当成
	// 目录绑定失败，也不能据物理字段猜出目录 ID。
	if !hasReference {
		return nil
	}
	for _, i := range missing {
		aliases := 0
		for _, query := range queries {
			if query.Alias == queries[i].Alias {
				aliases++
			}
		}
		if aliases != 1 {
			return descriptionFailure("strategy_revision_metric_binding_invalid")
		}
	}
	references := make(map[int]int64, len(missing))
	for _, i := range missing {
		query := queries[i]
		field := query.MetricField
		if field == "" {
			field = spec.FieldName
		}
		matches, reference := 0, int64(0)
		for _, candidate := range runtime.Queries {
			// 单条无别名查询仍须证明字段一致；多条缺少别名不能靠数组
			// 顺序猜测，尤其不能把衍生展开的首个成员当成原始指标。
			if candidate.Alias != query.Alias || (query.Alias == "" && len(queries) != 1) {
				continue
			}
			matches++
			if field == "" || candidate.MetricField != field || candidate.MetricRefID <= 0 {
				return descriptionFailure("strategy_revision_metric_binding_invalid")
			}
			reference = candidate.MetricRefID
		}
		if matches != 1 {
			return descriptionFailure("strategy_revision_metric_binding_invalid")
		}
		references[i] = reference
	}
	for i, reference := range references {
		queries[i].MetricRefID = reference
	}
	return nil
}

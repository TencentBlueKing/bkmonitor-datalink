package datasources

import (
	"encoding/json"

	"linkd/internal/enrich/models"
)

// bindPublishedMetricReferences 只补齐声明查询缺失的目录身份，不把编译后的
// 表达式、条件或展开成员覆盖回声明。别名和物理字段必须唯一对应；多业务
// 投影必须给出相同身份，否则无法证明正在丰富的是同一指标。
func bindPublishedMetricReferences(spec *models.CWStrategySpec, payload strategyPublication) error {
	if spec.StrategyItem == nil || spec.FieldTag == models.CWStrategyFieldTagDerivedMetric ||
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
	if len(queries) > 32 || len(payload.Runtime) != len(payload.Resolved) {
		return descriptionFailure("publication_metric_binding_invalid")
	}
	for _, i := range missing {
		aliases := 0
		for _, query := range queries {
			if query.Alias == queries[i].Alias {
				aliases++
			}
		}
		if aliases != 1 {
			return descriptionFailure("publication_metric_binding_invalid")
		}
	}
	references := make(map[int]int64, len(missing))
	for business, raw := range payload.Runtime {
		// 只解码身份字段；运行时 index_set_id 等 DTO 与声明查询不同，
		// 不相关字段的类型不能阻断普通 Enrich。
		var runtime struct {
			Error   json.RawMessage `json:"error"`
			Queries []struct {
				Alias       string `json:"alias"`
				MetricField string `json:"metric_field"`
				MetricRefID int64  `json:"metric_ref_id"`
			} `json:"query_configs"`
		}
		if json.Unmarshal(raw, &runtime) != nil || len(runtime.Error) != 0 || len(runtime.Queries) != len(queries) {
			return descriptionFailure("publication_metric_binding_invalid")
		}
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
					return descriptionFailure("publication_metric_binding_invalid")
				}
				reference = candidate.MetricRefID
			}
			if matches != 1 || (business > 0 && references[i] != reference) {
				return descriptionFailure("publication_metric_binding_invalid")
			}
			references[i] = reference
		}
	}
	for i, reference := range references {
		queries[i].MetricRefID = reference
	}
	return nil
}

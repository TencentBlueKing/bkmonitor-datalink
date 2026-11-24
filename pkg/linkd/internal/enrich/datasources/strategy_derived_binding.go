package datasources

import (
	"encoding/json"
	"linkd/internal/enrich/models"
)

// bindRevisionDerivedKind 从同一不可变执行快照确认 catalog 父指标的衍生类型。
// 编译后成员的引用只证明父定义类型，不能替换声明的父引用、表达式或查询条件。
func bindRevisionDerivedKind(spec *models.CWStrategySpec, items []json.RawMessage) error {
	if spec.StrategyItem == nil || len(spec.StrategyItem.QueryConfigs) != 1 || len(items) != 1 {
		return nil
	}
	query := spec.StrategyItem.QueryConfigs[0]
	if query.QueryConfigType != "catalog" || query.MetricRefID <= 0 {
		return nil
	}
	var runtime struct {
		Error   json.RawMessage `json:"error"`
		Queries []struct {
			Snapshot struct {
				ParentID int64 `json:"parent_metric_id"`
				Derived  *struct {
					ID    int64  `json:"metric_id"`
					Field string `json:"metric_field"`
				} `json:"derived"`
			} `json:"metric_snapshot"`
		} `json:"query_configs"`
	}
	if json.Unmarshal(items[0], &runtime) != nil || len(runtime.Error) > 0 || len(runtime.Queries) > 32 {
		return descriptionFailure("strategy_revision_metric_binding_invalid")
	}
	found := false
	for _, member := range runtime.Queries {
		snapshot := member.Snapshot
		if snapshot.Derived == nil {
			continue
		}
		found = true
		field := query.MetricField
		if field == "" {
			field = spec.FieldName
		}
		if snapshot.ParentID != query.MetricRefID || snapshot.Derived.ID != query.MetricRefID || field == "" || snapshot.Derived.Field != field {
			return descriptionFailure("strategy_revision_metric_binding_invalid")
		}
	}
	if !found {
		return nil
	}
	// 同一父指标的展开不能混入缺少父身份的查询。
	for _, member := range runtime.Queries {
		if member.Snapshot.Derived == nil {
			return descriptionFailure("strategy_revision_metric_binding_invalid")
		}
	}
	spec.FieldTag = models.CWStrategyFieldTagDerivedMetric
	return nil
}

package enrich

import (
	"context"
	"linkd/internal/domain"
	"linkd/internal/enrich/models"
	"testing"
)

type spaceStrategyReader struct{ space string }

func (r spaceStrategyReader) GetByStrategyID(context.Context, models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	return models.CWStrategy{SpaceUID: r.space}, true, nil
}

func TestMetricReverseLookupUsesConcretePublishedSpace(t *testing.T) {
	for _, space := range []string{"bkci__blueking", "", "*"} {
		t.Run(space, func(t *testing.T) {
			event := testAlert()
			event.Labels["bk_biz_id"], _ = domain.NewNumberScalar(-255)
			reader := &scopeCacheReader{}
			scope, err := NewScope(event, Sources{CWStrategy: spaceStrategyReader{space: space}, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = scope.MetricMetadata(t.Context(), models.MetricQuery{SpaceUID: "bkcc__untrusted", TableID: "k8s", FieldName: "usage"})
			if space != "" {
				if err != nil || reader.metricCalls != 1 || reader.metricQueries[0].SpaceUID != space || reader.metricQueries[0].TenantID != event.BKTenantID {
					t.Fatalf("queries=%+v error=%v", reader.metricQueries, err)
				}
			} else if err == nil || reader.metricCalls != 0 {
				t.Fatalf("unscoped lookup allowed: queries=%+v error=%v", reader.metricQueries, err)
			}
		})
	}
}

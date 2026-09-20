// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package rules

import (
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich/models"
)

func TestClassifyCachesBaseTargetBranchOnlyForTargetStrategies(t *testing.T) {
	t.Parallel()
	baseModel := HostModelCode
	base := models.CWStrategy{ObjectModelCode: &baseModel}
	if got := Classify(base, nil); got.Main != MainTarget || got.BaseTarget != BaseTargetSystemMetric {
		t.Fatalf("classification=%#v", got)
	}
	uptimeModel := UptimeModelCode
	uptime := models.CWStrategy{ObjectModelCode: &uptimeModel}
	if got := Classify(uptime, domain.DimensionMap{FieldBKInstID: numberScalar(t, 101)}); got.Main != MainUptimeCheck || got.BaseTarget != BaseTargetUptimeCheck {
		t.Fatalf("uptime classification=%#v", got)
	}
	data := base
	data.Spec.ConfigType = models.CWStrategyConfigTypeData
	if got := Classify(data, nil); got.Main != MainData || got.BaseTarget != "" {
		t.Fatalf("classification=%#v", got)
	}
}

func numberScalar(t *testing.T, value float64) domain.Scalar {
	t.Helper()
	result, err := domain.NewNumberScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestClassifyBaseTarget(t *testing.T) {
	t.Parallel()
	number := func(value float64) domain.Scalar {
		result, err := domain.NewNumberScalar(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	cases := []struct {
		name       string
		model      string
		dimensions domain.DimensionMap
		want       BaseTargetBranch
	}{
		{name: "monitor source by instance", dimensions: domain.DimensionMap{FieldBKInstID: number(101)}, want: BaseTargetMonitorSource},
		{name: "monitor source by object model", dimensions: domain.DimensionMap{FieldObjectModelID: domain.NewStringScalar("host"), FieldObjectModelInstID: number(101)}, want: BaseTargetMonitorSource},
		{name: "collect task", dimensions: domain.DimensionMap{FieldBKCollectConfigID: domain.NewStringScalar("collect-1")}, want: BaseTargetCollectTask},
		{name: "no data", dimensions: domain.DimensionMap{FieldNoDataDimension: domain.NewBoolScalar(true), FieldModelID: domain.NewStringScalar("cw-Service"), FieldModelInstID: number(2)}, want: BaseTargetNoData},
		{name: "model identity without no data marker", dimensions: domain.DimensionMap{FieldModelID: domain.NewStringScalar("cw-Service"), FieldModelInstID: number(2)}, want: BaseTargetBasic},
		{name: "false no data marker", model: HostModelCode, dimensions: domain.DimensionMap{FieldNoDataDimension: domain.NewBoolScalar(false), FieldModelID: domain.NewStringScalar(HostModelCode)}, want: BaseTargetSystemMetric},
		{name: "system metric", model: HostModelCode, want: BaseTargetSystemMetric},
		{name: "uptime is classified by Classify", model: UptimeModelCode, want: BaseTargetBasic},
		{name: "zero values fall through", model: HostModelCode, dimensions: domain.DimensionMap{
			FieldBKInstID: number(0), FieldBKCollectConfigID: domain.NewStringScalar(""), FieldModelID: domain.NewStringScalar(""),
		}, want: BaseTargetSystemMetric},
		{name: "basic fallback", model: "cw-Disk", want: BaseTargetBasic},
		{name: "priority monitor before collect", model: HostModelCode, dimensions: domain.DimensionMap{FieldBKInstID: number(101), FieldBKCollectConfigID: number(202)}, want: BaseTargetMonitorSource},
		{name: "priority collect before no data", model: HostModelCode, dimensions: domain.DimensionMap{FieldBKCollectConfigID: number(202), FieldNoDataDimension: domain.NewBoolScalar(true), FieldModelID: domain.NewStringScalar(HostModelCode)}, want: BaseTargetCollectTask},
		{name: "priority no data before system metric", model: HostModelCode, dimensions: domain.DimensionMap{FieldNoDataDimension: domain.NewBoolScalar(true), FieldModelID: domain.NewStringScalar(HostModelCode)}, want: BaseTargetNoData},
		{name: "legacy model key is not a NoData trigger", model: HostModelCode, dimensions: domain.DimensionMap{"cw_object_model_id": number(303)}, want: BaseTargetSystemMetric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyBaseTarget(tc.model, tc.dimensions); got != tc.want {
				t.Fatalf("branch=%q, want %q", got, tc.want)
			}
		})
	}
}

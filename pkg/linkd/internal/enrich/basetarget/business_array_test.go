package basetarget

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestBaseTargetRecognizesBusinessArrays(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		ids     any
		wantBiz string
	}{
		{name: "JSON array", ids: []any{json.Number("2")}, wantBiz: "2"},
		{name: "int64 array", ids: []int64{2}, wantBiz: "2"},
		{name: "int array", ids: []int{2}, wantBiz: "2"},
		{name: "multiple businesses keep source business", ids: []any{json.Number("2"), json.Number("3")}, wantBiz: "2"},
		{name: "empty array allows topology fallback", ids: []any{}, wantBiz: "9"},
		{name: "missing array allows topology fallback", wantBiz: "9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := serviceReader()
			reader.model.Fields[rules.FieldHostRelatedField] = "service_run_host"
			reader.instance.Attributes[rules.FieldBKBizID] = nil
			reader.instance.Fields["bk_biz_ids"] = tc.ids
			reader.relatedHost, reader.relatedFound = hostReader().instance, true
			reader.topology, reader.topologyFound = models.ResourceTopology{BKBizID: 9, BKBizName: "关联主机业务"}, true
			dimensions := domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-Service"), rules.FieldModelInstID: domain.NewStringScalar("2")}
			event := baseTargetAlert(t, dimensions)
			original := event.Clone()
			scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader}, enrich.ScopeOptions{CMDBSetModuleEnabled: true})
			if err != nil {
				t.Fatal(err)
			}
			code := "cw-Service"
			result, err := Enrich(t.Context(), scope, rules.Classification{BaseTarget: rules.BaseTargetMonitorSource}, models.CWStrategy{ObjectModelCode: &code}, dimensions, 2, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := rules.FirstStringFieldValue(map[string]any{"id": result.Resource.BKBizID}, "id"); got != tc.wantBiz || len(result.ResourceDiagnostics) != 0 {
				t.Fatalf("resource=%+v diagnostics=%+v", result.Resource, result.ResourceDiagnostics)
			}
			if tc.wantBiz == "2" && reader.topologyCalls != 0 {
				t.Fatal("business array triggered host business lookup")
			}
			if !reflect.DeepEqual(event, original) {
				t.Fatal("source Event changed")
			}
		})
	}
}

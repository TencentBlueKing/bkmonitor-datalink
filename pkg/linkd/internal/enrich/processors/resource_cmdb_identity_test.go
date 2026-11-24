package processors

import (
	"context"
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

// 规范实例不携带 attributes.bk_inst_id；保留其他业务属性也不能改变实例身份。
func TestResourceCMDBCanonicalInstanceWithoutLegacyAttribute(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ object, model, id string }{
		{"mysql", "cw-MySQL", "191669"},
		{"host", "cw-Host", "3"},
		{"biz", "cw-Biz", "524"},
		{"set", "cw-Set", "4"},
		{"module", "cw-Module", "5"},
		{"service_instance", "cw-Service", "6"},
	} {
		for _, input := range []string{"subject", "extra_data", "mixed"} {
			t.Run(tc.object+"/"+input, func(t *testing.T) {
				event := processorBaseTargetAlert(t, domain.DimensionMap{})
				event.Labels = domain.DimensionMap{}
				if input == "subject" {
					event.SubjectSystem, event.SubjectType, event.SubjectID = "cmdb", tc.object, tc.id
				} else {
					event.ExtraData = domain.JSONObject{"bk_obj_id": json.RawMessage(`"` + tc.object + `"`), "bk_inst_id": json.RawMessage(`"` + tc.id + `"`)}
					if input == "mixed" {
						event.Labels["model_id"] = domain.NewStringScalar(tc.model)
						event.Labels["model_inst_id"] = domain.NewStringScalar(tc.id)
					}
				}
				reader := cmdbCanonicalSnapshotReader{model: processorModel(event.BKTenantID, tc.model, tc.object, tc.object), instance: enrich.Instance{
					TenantID: event.BKTenantID, ModelCode: tc.model, InstanceID: tc.id,
					Fields: map[string]any{"entity_uid": tc.model + "|" + tc.id}, Attributes: map[string]any{},
				}}
				scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader})
				if err != nil {
					t.Fatal(err)
				}
				result, err := (Resource{}).Process(t.Context(), scope)
				if err != nil || result.Status != domain.EnrichStatusSucceeded || string(result.Value["model_inst_id"]) != `"`+tc.id+`"` || string(result.Value["bk_inst_id"]) != `"`+tc.id+`"` {
					t.Fatalf("canonical CMDB identity: result=%+v err=%v", result, err)
				}
			})
		}
	}
}

type cmdbCanonicalSnapshotReader struct {
	model    enrich.Model
	instance enrich.Instance
}

func (r cmdbCanonicalSnapshotReader) GetModelByCode(_ context.Context, tenant, model string) (enrich.Model, bool, error) {
	return r.model, tenant == r.model.TenantID && model == r.model.ModelCode, nil
}

func (r cmdbCanonicalSnapshotReader) GetModelByCMDBObjectID(_ context.Context, tenant, object string) (enrich.Model, bool, error) {
	return r.model, tenant == r.model.TenantID && object == resourceModelObjectID(r.model), nil
}

func (r cmdbCanonicalSnapshotReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	return r.instance, tenant == r.instance.TenantID && query.ModelCode == r.instance.ModelCode && query.InstanceID == r.instance.InstanceID && len(query.AttributeFilters) == 0, nil
}

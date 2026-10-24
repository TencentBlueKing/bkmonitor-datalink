package processors

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

func TestResourceSubjectWithoutStrategy(t *testing.T) {
	t.Parallel()
	event := processorBaseTargetAlert(t, domain.DimensionMap{})
	event.Labels = domain.DimensionMap{}
	event.SubjectSystem, event.SubjectType, event.SubjectID = "onemodel", "cw-Host", "101"
	reader := &subjectResourceReader{}
	scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Resource{}).Process(t.Context(), scope)
	if err != nil || result.Status != domain.EnrichStatusSucceeded ||
		string(result.Value["model_id"]) != `"cw-Host"` || string(result.Value["model_inst_id"]) != `"101"` {
		t.Fatalf("subject-only resource: status=%s value=%v diagnostics=%v err=%v", result.Status, result.Value, result.Diagnostics, err)
	}
}

type subjectResourceReader struct {
	modelError, instanceError                       error
	missingModel, missingInstance                   bool
	wrongTenant, wrongEntity, wrongModel, wrongCMDB bool
	canonicalID                                     string
	queries                                         []enrich.InstanceQuery
}

func (r *subjectResourceReader) GetModelByCode(_ context.Context, tenant, code string) (enrich.Model, bool, error) {
	object := "host"
	if code == "custom-device" {
		object = ""
	}
	if r.wrongTenant {
		tenant = "other-tenant"
	}
	if r.wrongModel {
		code = "other-model"
	}
	return processorModel(tenant, code, "主机", object), !r.missingModel, r.modelError
}

func (r *subjectResourceReader) GetModelByCMDBObjectID(ctx context.Context, tenant, object string) (enrich.Model, bool, error) {
	code := "cw-Host"
	if object == "router" {
		code = "network-device"
	}
	if object == "service_instance" {
		code = "cw-Service"
	}
	model, found, err := r.GetModelByCode(ctx, tenant, code)
	model.Fields["bk_cmdb_obj_id"] = object
	return model, found, err
}

func (r *subjectResourceReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.queries = append(r.queries, query)
	id := r.canonicalID
	if id == "" {
		id = "101"
	}
	entity := query.ModelCode + "|" + id
	if r.wrongEntity {
		entity = "wrong"
	}
	cmdbID := 101
	if r.wrongCMDB {
		cmdbID = 102
	}
	attribute := "bk_host_id"
	if len(query.AttributeFilters) != 0 {
		attribute = query.AttributeFilters[0].Field
	}
	return enrich.Instance{TenantID: tenant, ModelCode: query.ModelCode, InstanceID: id,
		Fields:     map[string]any{"entity_uid": entity, "display_name": "主机 101"},
		Attributes: map[string]any{attribute: cmdbID}}, !r.missingInstance, r.instanceError
}

func TestResourceIdentityInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, system, model, instance, root, raw string
		wantAttribute, wantModel                 string
		canonical                                bool
	}{
		{name: "CMDB subject", system: "cmdb", model: "host", instance: "101"},
		{name: "CMDB custom object", system: "cmdb", model: "router", instance: "101", wantAttribute: "bk_inst_id", wantModel: "network-device"},
		{name: "CMDB service instance", system: "cmdb", model: "service_instance", instance: "101", wantAttribute: "service_instance_id", wantModel: "cw-Service"},
		{name: "OneModel custom model", system: "onemodel", model: "custom-device", instance: "host|101"},
		{name: "canonical labels", root: "labels", raw: `{"model_id":"cw-Host","model_inst_id":"host|101"}`, canonical: true},
		{name: "canonical extra", root: "extra_data", raw: `{"model_id":"cw-Host","model_inst_id":"host|101"}`, canonical: true},
		{name: "canonical dimensions", root: "dimensions", raw: `{"model_id":"cw-Host","model_inst_id":"host|101"}`, canonical: true},
		{name: "legacy labels", root: "labels", raw: `{"bk_obj_id":"host","bk_inst_id":101}`},
		{name: "legacy extra", root: "extra_data", raw: `{"bk_obj_id":"host","bk_inst_id":"101"}`},
		{name: "legacy dimensions", root: "dimensions", raw: `{"bk_obj_id":"host","bk_inst_id":101}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			event.Labels = domain.DimensionMap{}
			event.SubjectSystem, event.SubjectType, event.SubjectID = tc.system, tc.model, tc.instance
			if tc.root != "" {
				var err error
				switch tc.root {
				case "labels":
					err = json.Unmarshal([]byte(tc.raw), &event.Labels)
				case "extra_data":
					err = json.Unmarshal([]byte(tc.raw), &event.ExtraData)
				case "dimensions":
					err = json.Unmarshal([]byte(tc.raw), &event.Dimensions)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before := event.Clone()
			reader := &subjectResourceReader{canonicalID: "host|101"}
			scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Resource{}).Process(t.Context(), scope)
			if err != nil || result.Status != domain.EnrichStatusSucceeded || string(result.Value["model_inst_id"]) != `"host|101"` || len(reader.queries) != 1 {
				t.Fatalf("result=%+v queries=%+v error=%v", result, reader.queries, err)
			}
			query := reader.queries[0]
			if tc.wantModel != "" && string(result.Value["model_id"]) != `"`+tc.wantModel+`"` {
				t.Fatalf("canonical model=%s", result.Value["model_id"])
			}
			if tc.system == "cmdb" || tc.root != "" && !tc.canonical {
				attribute := tc.wantAttribute
				if attribute == "" {
					attribute = "bk_host_id"
				}
				if query.InstanceID != "" || len(query.AttributeFilters) != 1 || query.AttributeFilters[0].Field != attribute || query.AttributeFilters[0].Value != int64(101) {
					t.Fatalf("CMDB query=%+v", query)
				}
			}
			if tc.model == "custom-device" {
				if _, found := result.Value["bk_obj_id"]; found {
					t.Fatal("OneModel-only resource invented CMDB identity")
				}
			}
			for _, field := range []string{"cw_labels", "dynamic_group_id", "bk_biz_id", "bk_cloud_id"} {
				if _, found := result.Value[field]; found {
					t.Fatalf("resource owns unrelated field %s", field)
				}
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("source event changed")
			}
		})
	}
}

func TestResourceInvalidAndConflictingIdentities(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		configure   func(*domain.Event)
		wantQueries int
	}{
		{name: "incomplete canonical blocks subject", configure: func(e *domain.Event) { e.Labels["model_id"] = domain.NewStringScalar("cw-Host") }},
		{name: "cross root pair rejected", configure: func(e *domain.Event) {
			e.Labels["model_id"] = domain.NewStringScalar("cw-Host")
			e.Dimensions["model_inst_id"] = domain.NewStringScalar("101")
		}},
		{name: "unknown namespace", configure: func(e *domain.Event) { e.SubjectSystem = "unknown" }},
		{name: "incomplete subject blocks legacy", configure: func(e *domain.Event) {
			e.SubjectID = ""
			e.ExtraData = domain.JSONObject{"bk_obj_id": json.RawMessage(`"host"`), "bk_inst_id": json.RawMessage(`101`)}
		}},
		{name: "boolean legacy ID", configure: func(e *domain.Event) {
			e.ExtraData = domain.JSONObject{"bk_obj_id": json.RawMessage(`"host"`), "bk_inst_id": json.RawMessage(`true`)}
		}},
		{name: "float legacy ID", configure: func(e *domain.Event) {
			e.ExtraData = domain.JSONObject{"bk_obj_id": json.RawMessage(`"host"`), "bk_inst_id": json.RawMessage(`1.5`)}
		}},
		{name: "subject conflict", configure: func(e *domain.Event) {
			e.Labels["model_id"] = domain.NewStringScalar("custom-device")
			e.Labels["model_inst_id"] = domain.NewStringScalar("101")
		}, wantQueries: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			event.Labels = domain.DimensionMap{}
			event.SubjectSystem, event.SubjectType, event.SubjectID = "onemodel", "cw-Host", "101"
			tc.configure(&event)
			reader := &subjectResourceReader{}
			scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Resource{}).Process(t.Context(), scope)
			if err != nil || result.Status != domain.EnrichStatusFailed || len(result.Value) != 0 || len(result.Diagnostics) == 0 || len(reader.queries) != tc.wantQueries {
				t.Fatalf("result=%+v queries=%v error=%v", result, reader.queries, err)
			}
		})
	}
}

func TestResourceDependenciesAndCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		reader     subjectResourceReader
		wantError  bool
		wantStatus domain.EnrichStatus
	}{
		{name: "model missing", reader: subjectResourceReader{missingModel: true}, wantStatus: domain.EnrichStatusFailed},
		{name: "instance missing keeps model", reader: subjectResourceReader{missingInstance: true}, wantStatus: domain.EnrichStatusPartial},
		{name: "model query failure", reader: subjectResourceReader{modelError: errors.New("query unavailable")}, wantError: true},
		{name: "instance query failure", reader: subjectResourceReader{instanceError: errors.New("query unavailable")}, wantError: true},
		{name: "wrong tenant", reader: subjectResourceReader{wrongTenant: true}, wantError: true},
		{name: "wrong model", reader: subjectResourceReader{wrongModel: true}, wantError: true},
		{name: "wrong entity", reader: subjectResourceReader{wrongEntity: true}, wantError: true},
		{name: "wrong CMDB ID", reader: subjectResourceReader{wrongCMDB: true}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			event.Labels = domain.DimensionMap{}
			event.SubjectSystem, event.SubjectType, event.SubjectID = "cmdb", "host", "101"
			if tc.reader.wrongModel {
				event.SubjectSystem, event.SubjectType = "onemodel", "cw-Host"
			}
			scope, err := enrich.NewScope(event, enrich.Sources{Model: &tc.reader, OneModel: &tc.reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Resource{}).Process(t.Context(), scope)
			if (err != nil) != tc.wantError || !tc.wantError && result.Status != tc.wantStatus {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if result.Status == domain.EnrichStatusPartial && (string(result.Value["model_id"]) != `"cw-Host"` || result.Value["model_inst_id"] != nil) {
				t.Fatal("missing instance was bound")
			}
		})
	}
	event := processorBaseTargetAlert(t, domain.DimensionMap{})
	event.SubjectSystem, event.SubjectType, event.SubjectID = "onemodel", "cw-Host", "101"
	reader := &subjectResourceReader{}
	scope, _ := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (Resource{}).Process(ctx, scope); !errors.Is(err, context.Canceled) || len(reader.queries) != 0 {
		t.Fatalf("cancel error=%v queries=%v", err, reader.queries)
	}
}

func TestResourceWithoutIdentitySkips(t *testing.T) {
	t.Parallel()
	event := processorBaseTargetAlert(t, domain.DimensionMap{})
	event.Labels = domain.DimensionMap{}
	scope, err := enrich.NewScope(event, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Resource{}).Process(t.Context(), scope)
	if err != nil || result.Status != domain.EnrichStatusSkipped {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

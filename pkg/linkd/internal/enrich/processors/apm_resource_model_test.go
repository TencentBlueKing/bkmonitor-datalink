// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
)

func TestAPMResourceModelName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		change   func(*enrich.Model)
		missing  bool
		noReader bool
		readErr  error
		wantName string
	}{
		{name: "registry display name", wantName: "定制服务实例"},
		{name: "model missing", missing: true},
		{name: "reader missing", noReader: true},
		{name: "read error", readErr: errors.New("model unavailable")},
		{name: "other tenant", change: func(m *enrich.Model) { m.TenantID = "tenant-b" }},
		{name: "other model", change: func(m *enrich.Model) { m.ModelCode = "cw-service" }},
		{name: "empty identity", change: func(m *enrich.Model) { m.ModelID = "" }},
		{name: "empty name", change: func(m *enrich.Model) { m.Fields["object_model_name"] = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := apmTestReader{}
			event := processorBaseTargetAlert(t, domain.DimensionMap{
				rules.FieldServiceName:   domain.NewStringScalar("checkout"),
				rules.FieldAPMInstanceID: domain.NewStringScalar("instance-a"),
			})
			before := event.Clone()
			model := enrich.Model{TenantID: "tenant-a", ModelID: "4", ModelCode: "cw-service_instance", Fields: map[string]any{
				"object_model_name": "定制服务实例", "bk_cmdb_obj_id": "unrelated-model-field",
			}}
			if tc.change != nil {
				tc.change(&model)
			}
			sources := enrich.Sources{CWStrategy: reader, OneModel: reader, APMApplication: reader, Business: reader}
			if !tc.noReader {
				sources.Model = apmModelNameReader(func(_ context.Context, tenant, code string) (enrich.Model, bool, error) {
					if tenant != event.BKTenantID || code != "cw-service_instance" {
						t.Fatalf("model lookup scope=%s/%s", tenant, code)
					}
					return model, !tc.missing, tc.readErr
				})
			}
			scope, err := enrich.NewScope(event, sources)
			if err != nil {
				t.Fatal(err)
			}
			result, err := (MonitorResource{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			wantName := `"` + tc.wantName + `"`
			if string(result.Value["model_name"]) != wantName || string(result.Value["model_id"]) != `"cw-service_instance"` || string(result.Value["model_inst_id"]) != `"17|checkout|instance-a"` {
				t.Fatalf("resource=%+v", result)
			}
			wantStatus := domain.EnrichStatusSucceeded
			if tc.wantName == "" {
				wantStatus = domain.EnrichStatusPartial
				if !hasDiagnosticDependency(result.Diagnostics, rules.DependencyOneModel) {
					t.Fatalf("missing model diagnostic: %+v", result)
				}
			}
			if result.Status != wantStatus || string(result.Value["bk_obj_id"]) != "null" {
				t.Fatalf("resource=%+v", result)
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("APM resource enrichment changed the source Event")
			}
		})
	}
}

func TestAPMResourceModelLookupCancellation(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scope, err := enrich.NewScope(processorBaseTargetAlert(t, nil), enrich.Sources{
		CWStrategy: reader, OneModel: reader, APMApplication: reader, Business: reader,
		Model: apmModelNameReader(func(context.Context, string, string) (enrich.Model, bool, error) {
			cancel()
			return enrich.Model{}, false, context.Canceled
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (MonitorResource{}).Process(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
}

type apmModelNameReader func(context.Context, string, string) (enrich.Model, bool, error)

func (read apmModelNameReader) GetModelByCode(ctx context.Context, tenant, code string) (enrich.Model, bool, error) {
	return read(ctx, tenant, code)
}

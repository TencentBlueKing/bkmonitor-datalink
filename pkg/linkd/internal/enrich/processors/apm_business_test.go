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
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestAPMGlobalBusinessUsesApplicationBusiness(t *testing.T) {
	reads := 0
	reader := apmBusinessTestReader{apmCatalogTestReader: apmCatalogTestReader{
		strategy:      apmCatalogStrategy(t),
		apmTestReader: apmTestReader{applicationReads: &reads, apps: []models.APMApplication{{TenantID: "tenant-a", ID: 54, Name: "demo", BKBizID: 2}}},
	}, global: true, found: true}
	event := processorBaseTargetAlert(t, domain.DimensionMap{
		"app_name":               domain.NewStringScalar("demo"),
		rules.FieldServiceName:   domain.NewStringScalar("kingeye-web"),
		rules.FieldAPMInstanceID: domain.NewStringScalar("instance-a"),
	})
	event.Labels["bk_biz_id"] = processorNumber(t, 524)
	before := event.Clone()
	chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, APM{}, Display{}}, enrich.Sources{
		CWStrategy: reader, Business: reader, APMApplication: reader, OneModel: reader, Model: reader, Metric: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{rules.MonitorResourceProcessor, rules.APMProcessor} {
		output := payload.Processors[i][name]
		if output.Status != domain.EnrichStatusSucceeded || string(output.Value["bk_biz_id"]) != "2" || string(output.Value["model_inst_id"]) != `"54|kingeye-web|instance-a"` {
			t.Fatalf("%s: %+v", name, output)
		}
		if string(output.Value["cw_labels"]) != `["bk_biz_id","bk_biz_id|2","apm_app_id","apm_app_id|54"]` {
			t.Fatalf("wrong application business labels: %+v", output)
		}
	}
	if string(payload.Processors[2][rules.DisplayProcessor].Value["object"]) != `"kingeye-web"` {
		t.Fatal("APM object missing")
	}
	if reads != 1 {
		t.Fatalf("application reads=%d, want 1", reads)
	}
	if !reflect.DeepEqual(event, before) {
		t.Fatal("source event mutated")
	}
}

func TestAPMBusinessScopeFailures(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		global, found, noBusinessReader bool
		businessErr                     error
		apps                            []models.APMApplication
		wantDependency                  string
		wantReads                       int
	}{
		{name: "ordinary business cannot cross", found: true, apps: []models.APMApplication{{TenantID: "tenant-a", ID: 54, Name: "demo", BKBizID: 2}}, wantDependency: rules.DependencyAPMApplication, wantReads: 1},
		{name: "global name ambiguous", global: true, found: true, apps: []models.APMApplication{{TenantID: "tenant-a", ID: 54, Name: "demo", BKBizID: 2}, {TenantID: "tenant-a", ID: 55, Name: "demo", BKBizID: 3}}, wantDependency: rules.DependencyAPMApplication, wantReads: 1},
		{name: "global cannot cross tenant", global: true, found: true, apps: []models.APMApplication{{TenantID: "other", ID: 54, Name: "demo", BKBizID: 2}}, wantDependency: rules.DependencyAPMApplication, wantReads: 1},
		{name: "global application missing", global: true, found: true, apps: []models.APMApplication{}, wantDependency: rules.DependencyAPMApplication, wantReads: 1},
		{name: "business missing", wantDependency: rules.DependencyBusiness},
		{name: "business reader missing", noBusinessReader: true, wantDependency: rules.DependencyBusiness},
		{name: "business read error", businessErr: errors.New("business unavailable"), wantDependency: rules.DependencyBusiness},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range []enrich.Processor{MonitorResource{}, APM{}} {
				reads := 0
				reader := apmBusinessTestReader{apmCatalogTestReader: apmCatalogTestReader{strategy: apmCatalogStrategy(t), apmTestReader: apmTestReader{apps: tc.apps, applicationReads: &reads}}, global: tc.global, found: tc.found, businessErr: tc.businessErr}
				event := processorBaseTargetAlert(t, domain.DimensionMap{"app_name": domain.NewStringScalar("demo")})
				event.Labels["bk_biz_id"] = processorNumber(t, 524)
				sources := enrich.Sources{CWStrategy: reader, APMApplication: reader, Metric: reader}
				if !tc.noBusinessReader {
					sources.Business = reader
				}
				scope, err := enrich.NewScope(event, sources)
				if err != nil {
					t.Fatal(err)
				}
				result, err := p.Process(t.Context(), scope)
				if err != nil || result.Status == domain.EnrichStatusSucceeded || !hasDiagnosticDependency(result.Diagnostics, tc.wantDependency) {
					t.Fatalf("%s: %+v err=%v", p.Name(), result, err)
				}
				if reads != tc.wantReads {
					t.Fatalf("%s: application reads=%d, want %d", p.Name(), reads, tc.wantReads)
				}
				if tc.global && string(result.Value["model_inst_id"]) != `""` {
					t.Fatalf("global failure invented an identity: %+v", result)
				}
			}
		})
	}
}

func TestAPMBusinessLookupCancellation(t *testing.T) {
	reader := apmBusinessTestReader{apmCatalogTestReader: apmCatalogTestReader{strategy: apmCatalogStrategy(t)}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	event := processorBaseTargetAlert(t, domain.DimensionMap{"app_name": domain.NewStringScalar("demo")})
	scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader, Business: apmBusinessReaderFunc(func(context.Context, string, int64) (bool, bool, error) {
		cancel()
		return false, false, context.Canceled
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (APM{}).Process(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestAPMGlobalBusinessDoesNotInferBusinessFromAppID(t *testing.T) {
	reader := apmBusinessTestReader{apmCatalogTestReader: apmCatalogTestReader{strategy: apmCatalogStrategy(t)}, global: true, found: true}
	event := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldAPMAppID: domain.NewStringScalar("54")})
	event.Labels["bk_biz_id"] = processorNumber(t, 524)
	scope, err := enrich.NewScope(event, enrich.Sources{Business: reader})
	if err != nil {
		t.Fatal(err)
	}
	application, diagnostics, err := resolveAPMApplication(t.Context(), scope, reader.strategy, "", 524)
	if err != nil || application.ID != "" || application.BKBizID != 0 || len(diagnostics) == 0 || diagnostics[0].Code != enrich.DiagnosticCodeMissingField {
		t.Fatalf("application=%+v diagnostics=%v error=%v", application, diagnostics, err)
	}
}

type apmBusinessReaderFunc func(context.Context, string, int64) (bool, bool, error)

func (r apmBusinessReaderFunc) IsGlobalBusiness(ctx context.Context, tenant string, biz int64) (bool, bool, error) {
	return r(ctx, tenant, biz)
}

type apmBusinessTestReader struct {
	apmCatalogTestReader
	global, found bool
	businessErr   error
}

func (r apmBusinessTestReader) IsGlobalBusiness(context.Context, string, int64) (bool, bool, error) {
	return r.global, r.found, r.businessErr
}

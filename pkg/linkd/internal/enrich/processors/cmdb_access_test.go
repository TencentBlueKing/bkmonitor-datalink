package processors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
)

type businessReader struct {
	tenant, id  string
	calls       int
	name        string
	found       bool
	err         error
	wrongTenant bool
}

func (r *businessReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.calls++
	if tenant != r.tenant || query.ModelCode != "cw-biz" || query.InstanceID != r.id {
		return enrich.Instance{}, false, errors.New("unexpected business query")
	}
	returnedTenant := tenant
	if r.wrongTenant {
		returnedTenant = "other"
	}
	return enrich.Instance{TenantID: returnedTenant, ModelCode: "cw-biz", InstanceID: r.id, Attributes: map[string]any{"bk_biz_name": r.name}}, r.found, r.err
}

type failingGroups struct{}

func (failingGroups) GetDynamicGroupIDs(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("groups unavailable")
}

func TestCMDBAccessBusinessName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, tenant, labels, extra, id, wantName            string
		found, wrongTenant, failQuery, failGroups, emptyName bool
		calls                                                int
		diagnostic                                           enrich.DiagnosticCode
	}{
		{name: "nonhost integer", tenant: "system", labels: `{"model_id":"cw-MySQL","bk_biz_id":2}`, extra: `{}`, id: "2", wantName: "业务二", found: true, calls: 1},
		{name: "host string", tenant: "system", labels: `{"model_id":"cw-Host","bk_biz_id":"2"}`, extra: `{}`, id: "2", wantName: "业务二", found: true, calls: 1},
		{name: "ID only", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":2}`, id: "2", wantName: "业务二", found: true, calls: 1},
		{name: "empty label name", tenant: "system", labels: `{"bk_biz_id":2,"bk_biz_name":""}`, extra: `{}`, id: "2", wantName: "业务二", found: true, calls: 1},
		{name: "null extra name", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":2,"bk_biz_name":null}`, id: "2", wantName: "业务二", found: true, calls: 1},
		{name: "labels ID wins", tenant: "system", labels: `{"bk_biz_id":"2"}`, extra: `{"bk_biz_id":3}`, id: "2", wantName: "业务二", found: true, calls: 1},
		{name: "explicit empty ID blocks fallback", tenant: "system", labels: `{"bk_biz_id":""}`, extra: `{"bk_biz_id":2}`, diagnostic: enrich.DiagnosticCodeInvalidField},
		{name: "existing name", tenant: "system", labels: `{"bk_biz_id":2,"bk_biz_name":"原名称"}`, extra: `{}`, wantName: "原名称"},
		{name: "existing extra name", tenant: "system", labels: `{"bk_biz_id":2}`, extra: `{"bk_biz_name":"原名称"}`, wantName: "原名称"},
		{name: "other tenant same ID", tenant: "tenant-b", labels: `{"bk_biz_id":2}`, extra: `{}`, id: "2", wantName: "业务二", found: true, calls: 1},
		{name: "cross tenant response rejected", tenant: "system", labels: `{"bk_biz_id":2}`, extra: `{}`, id: "2", found: true, wrongTenant: true, calls: 1, diagnostic: enrich.DiagnosticCodeDependencyInvalid},
		{name: "empty business name", tenant: "system", labels: `{"bk_biz_id":2}`, extra: `{}`, id: "2", found: true, emptyName: true, calls: 1, diagnostic: enrich.DiagnosticCodeMissingField},
		{name: "both dependencies fail", tenant: "system", labels: `{"bk_biz_id":2,"model_id":"cw-MySQL","model_inst_id":"22"}`, extra: `{}`, id: "2", failQuery: true, failGroups: true, calls: 1, diagnostic: enrich.DiagnosticCodeDependencyInvalid},
		{name: "not found", tenant: "system", labels: `{"bk_biz_id":2}`, extra: `{}`, id: "2", calls: 1, diagnostic: enrich.DiagnosticCodeMissingField},
		{name: "query fails preserves other patches", tenant: "system", labels: `{"bk_biz_id":2}`, extra: `{}`, id: "2", failQuery: true, calls: 1, diagnostic: enrich.DiagnosticCodeDependencyInvalid},
		{name: "group failure preserves name", tenant: "system", labels: `{"bk_biz_id":2,"model_id":"cw-MySQL","model_inst_id":"22"}`, extra: `{}`, id: "2", wantName: "业务二", found: true, calls: 1, failGroups: true, diagnostic: enrich.DiagnosticCodeDependencyInvalid},
		{name: "multiple IDs not picked", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":[2,3]}`, diagnostic: enrich.DiagnosticCodeInvalidField},
		{name: "large integer lossless", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":9007199254740993}`, id: "9007199254740993", wantName: "业务二", found: true, calls: 1},
		{name: "fraction rejected", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":2.5}`, diagnostic: enrich.DiagnosticCodeInvalidField},
		{name: "boolean rejected", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":true}`, diagnostic: enrich.DiagnosticCodeInvalidField},
		{name: "zero rejected", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":0}`, diagnostic: enrich.DiagnosticCodeInvalidField},
		{name: "negative rejected", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":-2}`, diagnostic: enrich.DiagnosticCodeInvalidField},
		{name: "overflow rejected", tenant: "system", labels: `{}`, extra: `{"bk_biz_id":"9223372036854775808"}`, diagnostic: enrich.DiagnosticCodeInvalidField},
		{name: "no ID", tenant: "system", labels: `{}`, extra: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := domain.Event{BKTenantID: tc.tenant, Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}}
			if err := json.Unmarshal([]byte(tc.labels), &event.Labels); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.extra), &event.ExtraData); err != nil {
				t.Fatal(err)
			}
			reader := &businessReader{tenant: tc.tenant, id: tc.id, name: "业务二", found: tc.found, wrongTenant: tc.wrongTenant}
			if tc.emptyName {
				reader.name = ""
			}
			if tc.failQuery {
				reader.err = errors.New("lookup unavailable")
			}
			sources := enrich.Sources{OneModel: reader}
			if tc.failGroups {
				sources.DynamicGroup = failingGroups{}
			}
			chain, err := enrich.NewChain([]enrich.Processor{CMDBAccess{}}, sources)
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event, Preview: true})
			if err != nil {
				t.Fatal(err)
			}
			event.Enrich = result.Data
			effective, err := view.EnrichedEvent(event, "warning")
			if err != nil {
				t.Fatal(err)
			}
			var name string
			if label, ok := effective.Labels["bk_biz_name"]; ok {
				name, _ = label.StringValue()
			} else {
				_ = json.Unmarshal(effective.ExtraData["bk_biz_name"], &name)
			}
			if name != tc.wantName || reader.calls != tc.calls {
				t.Fatalf("name=%q want=%q queries=%d want=%d", name, tc.wantName, reader.calls, tc.calls)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			step := payload.Processors[0]["cmdb-access"]
			if tc.diagnostic != "" {
				if step.Status != domain.EnrichStatusPartial || len(step.Diagnostics) == 0 || step.Diagnostics[0].Code != tc.diagnostic {
					t.Fatalf("status/diagnostics: %+v", step)
				}
			} else if result.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("status=%s: %+v", result.Status, step)
			}
			if tc.failQuery && tc.failGroups && len(step.Diagnostics) != 2 {
				t.Fatalf("one dependency hid the other diagnostic: %+v", step.Diagnostics)
			}
			if len(step.Patches) == 0 {
				t.Fatal("permission patch lost")
			}
		})
	}
}

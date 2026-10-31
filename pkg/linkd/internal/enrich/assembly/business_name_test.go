package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
	"linkd/internal/onemodel"
)

type businessBindingReader struct {
	ids   []any
	found bool
	model string
	name  string
}

func (r businessBindingReader) Search(_ context.Context, tenant string, q onemodel.Query) ([]onemodel.Instance, error) {
	if !r.found {
		return nil, nil
	}
	return []onemodel.Instance{{TenantID: tenant, ModelCode: q.ModelID, InstanceID: "22", Fields: map[string]any{"bk_biz_ids": r.ids}, Attributes: map[string]any{"bk_biz_name": r.name}}}, nil
}
func (businessBindingReader) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, nil
}

type businessLookup struct {
	id    string
	calls int
}

func (r *businessLookup) FindInstance(_ context.Context, tenant string, q enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.calls++
	if tenant != "system" || q.ModelCode != "cw-biz" || q.InstanceID != r.id {
		return enrich.Instance{}, false, fmt.Errorf("unexpected business identity")
	}
	return enrich.Instance{TenantID: tenant, ModelCode: q.ModelCode, InstanceID: q.InstanceID, Attributes: map[string]any{"bk_biz_name": "最终业务"}}, true, nil
}

func TestBusinessNameAfterCMDBAndFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, object, model, inputID, fieldID string
		ids                                   []any
		found                                 bool
		wantID                                string
		lookupName                            string
	}{
		{name: "nonhost no assignments", object: "mysql", model: "cw-MySQL", ids: []any{2}, found: true, wantID: "2"},
		{name: "host no assignments", object: "host", model: "cw-Host", ids: []any{2}, found: true, wantID: "2"},
		{name: "unmatched with input ID", object: "mysql", model: "cw-MySQL", inputID: "2", wantID: "2"},
		{name: "later fields ID wins", object: "mysql", model: "cw-MySQL", ids: []any{2}, found: true, fieldID: "3", wantID: "3"},
		{name: "input ID must not borrow instance name", object: "mysql", model: "cw-MySQL", inputID: "3", ids: []any{2}, found: true, wantID: "3", lookupName: "错误的实例业务"},
		{name: "multiple businesses remain ambiguous", object: "mysql", model: "cw-MySQL", ids: []any{2, 3}, found: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := map[string]any{"id": "bind", "identity": map[string]any{"bk_obj_id": tc.object, "model_name": "对象"}, "lookup": map[string]any{"model_id": tc.model, "expect": "first"}}
			configs := []config.EnrichProcessorConfig{{Type: "cmdb", Config: map[string]any{"rules": []any{rule}}}}
			if tc.fieldID != "" {
				configs = append(configs, config.EnrichProcessorConfig{Type: "fields", Config: map[string]any{"rules": []any{map[string]any{"id": "later", "operations": []any{map[string]any{"id": "biz", "type": "assign", "assignments": []any{map[string]any{"target": "$.labels.bk_biz_id", "value": map[string]any{"literal": tc.fieldID}}}}}}}}})
			}
			configs = append(configs, config.EnrichProcessorConfig{Type: "cmdb-access"})
			source := config.EventSource{EventSourceID: "mysql", Enrich: config.EnrichConfig{Processors: configs}}
			reader := &businessLookup{id: tc.wantID}
			router, err := NewRouter([]config.EventSource{source}, enrich.Sources{CMDB: businessBindingReader{ids: tc.ids, found: tc.found, model: tc.model, name: tc.lookupName}, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			event := domain.Event{BKTenantID: "system", EventSourceID: "mysql", Labels: domain.DimensionMap{"model_id": domain.NewStringScalar(tc.model), "model_inst_id": domain.NewStringScalar("22")}, Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}}
			if tc.inputID != "" {
				event.Labels["bk_biz_id"] = domain.NewStringScalar(tc.inputID)
			}
			result, err := router.Enrich(t.Context(), enrich.Input{Event: event, Preview: true})
			if err != nil {
				t.Fatal(err)
			}
			event.Enrich = result.Data
			effective, err := view.EnrichedEvent(event, "warning")
			if err != nil {
				t.Fatal(err)
			}
			var name string
			_ = json.Unmarshal(effective.ExtraData["bk_biz_name"], &name)
			if tc.wantID != "" {
				if name != "最终业务" || reader.calls != 1 {
					t.Fatalf("name=%q queries=%d result=%+v", name, reader.calls, result)
				}
			} else if name != "" || reader.calls != 0 {
				t.Fatalf("ambiguous business queried: %q calls=%d", name, reader.calls)
			}
		})
	}
}

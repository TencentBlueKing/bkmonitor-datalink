package kachook

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/processors"
)

type businessNameProjectionReader struct{}

func (businessNameProjectionReader) FindInstance(_ context.Context, tenant string, q enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if tenant != "tenant-1" || q.ModelCode != "cw-biz" || q.InstanceID != "2" {
		return enrich.Instance{}, false, fmt.Errorf("wrong effective business")
	}
	return enrich.Instance{TenantID: tenant, ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{"bk_biz_name": "业务二"}}, true, nil
}

func TestBusinessNameFinalizationReachesKACProjection(t *testing.T) {
	t.Parallel()
	for _, labels := range []string{`{"bk_biz_id":2,"bk_biz_name":""}`, `{"bk_biz_id":"2","bk_biz_name":""}`} {
		alert := testAlert()
		if err := json.Unmarshal([]byte(labels), &alert.Labels); err != nil {
			t.Fatal(err)
		}
		alert.ExtraData = domain.JSONObject{"bk_biz_id": json.RawMessage(`3`)}
		event := domain.Event{BKTenantID: alert.BKTenantID, Labels: alert.Labels, ExtraData: alert.ExtraData, Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}}
		chain, err := enrich.NewChain([]enrich.Processor{processors.CMDBAccess{}}, enrich.Sources{OneModel: businessNameProjectionReader{}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := chain.Enrich(t.Context(), enrich.Input{Event: event, Preview: true})
		if err != nil {
			t.Fatal(err)
		}
		alert.Enrich = result.Data.Evaluations[0].Data
		alert.EnrichStatus = result.Status
		message, err := CompatibilityMessage(alert, kacLevel)
		if err != nil {
			t.Fatal(err)
		}
		if message.BKBizID != "2" || message.BKBizName != "业务二" {
			t.Fatalf("KAC effective identity/name=%q/%q", message.BKBizID, message.BKBizName)
		}
	}
}

package processors

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

//go:embed testdata/resource_subject/onemodel_host_snapshot.json
var resourceHostSnapshot []byte

// 只重放脱敏实例快照；模型目录使用固定夹具，不访问线上、不投递消息。
func TestResourceCapturedOneModelSnapshotReplay(t *testing.T) {
	t.Parallel()
	var snapshot struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(resourceHostSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 2 {
		t.Fatal("captured sample count changed")
	}
	for index, expectedID := range []string{"10", "1000"} {
		var instance struct {
			Tenant     string         `json:"bk_tenant_id"`
			Model      string         `json:"model_id"`
			ID         string         `json:"model_inst_id"`
			UID        string         `json:"entity_uid"`
			Display    string         `json:"display_name"`
			Attributes map[string]any `json:"attributes"`
		}
		raw, _ := json.Marshal(snapshot.Items[index])
		if err := json.Unmarshal(raw, &instance); err != nil {
			t.Fatal(err)
		}
		if instance.ID != expectedID || instance.Model != "cw-Host" || instance.Tenant != "system" {
			t.Fatal("independent snapshot identity expectation changed")
		}
		for _, mode := range []string{"cmdb subject", "onemodel subject", "legacy extra", "legacy dimensions"} {
			t.Run(expectedID+"/"+mode, func(t *testing.T) {
				event := processorBaseTargetAlert(t, domain.DimensionMap{})
				event.BKTenantID, event.Labels = "system", domain.DimensionMap{}
				switch mode {
				case "cmdb subject":
					event.SubjectSystem, event.SubjectType, event.SubjectID = "cmdb", "host", expectedID
				case "onemodel subject":
					event.SubjectSystem, event.SubjectType, event.SubjectID = "onemodel", "cw-Host", expectedID
				case "legacy extra":
					event.ExtraData = domain.JSONObject{"bk_obj_id": json.RawMessage(`"host"`), "bk_inst_id": json.RawMessage(fmt.Sprintf("%q", expectedID))}
				case "legacy dimensions":
					event.Dimensions = domain.DimensionMap{"bk_obj_id": domain.NewStringScalar("host"), "bk_inst_id": domain.NewStringScalar(expectedID)}
				}
				reader := snapshotResourceReader{instance: enrich.Instance{TenantID: instance.Tenant, ModelCode: instance.Model, InstanceID: instance.ID, Fields: map[string]any{"entity_uid": instance.UID, "display_name": instance.Display}, Attributes: instance.Attributes}}
				chain, err := enrich.NewChain([]enrich.Processor{Resource{}, CMDBAccess{}}, enrich.Sources{Model: &reader, OneModel: &reader})
				if err != nil {
					t.Fatal(err)
				}
				result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
				if err != nil || result.Status != domain.EnrichStatusSucceeded {
					t.Fatalf("status=%s error=%v", result.Status, err)
				}
				payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
				if err != nil {
					t.Fatal(err)
				}
				resource := payload.Processors[0]["resource"]
				if string(resource.Value["model_id"]) != `"cw-Host"` || string(resource.Value["model_inst_id"]) != fmt.Sprintf("%q", expectedID) {
					t.Fatalf("resource=%v", resource.Value)
				}
				var labels []string
				if err := json.Unmarshal(payload.Processors[1]["cmdb-access"].Value["cw_labels"], &labels); err != nil || !slices.Contains(labels, "cw-Host|"+expectedID) {
					t.Fatalf("permission labels=%v err=%v", labels, err)
				}
			})
		}
	}
}

type snapshotResourceReader struct {
	subjectResourceReader
	instance enrich.Instance
}

func (r snapshotResourceReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if tenant != r.instance.TenantID || query.ModelCode != r.instance.ModelCode {
		return enrich.Instance{}, false, enrich.ErrInvalidDataSourceResponse
	}
	if query.InstanceID != "" && query.InstanceID != r.instance.InstanceID {
		return enrich.Instance{}, false, nil
	}
	for _, filter := range query.AttributeFilters {
		id, ok := resourceCMDBID(r.instance.Attributes[filter.Field])
		wanted, valid := resourceCMDBID(filter.Value)
		if !ok || !valid || id != wanted {
			return enrich.Instance{}, false, nil
		}
	}
	return r.instance, true, nil
}

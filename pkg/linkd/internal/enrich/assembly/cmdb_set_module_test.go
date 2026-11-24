package assembly

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/rules"
	"linkd/internal/onemodel"
)

func TestRouterCMDBSetModuleSwitch(t *testing.T) {
	for _, processor := range []string{"monitor-resource", "resource"} {
		for _, test := range []struct {
			name            string
			enabled         bool
			missingBusiness bool
			instanceFields  bool
			failMembership  bool
			wantCalls       int
			wantSet         string
			wantModule      string
			wantStatus      domain.EnrichStatus
		}{
			{name: "default skips topology", wantSet: "null", wantModule: "null", wantStatus: domain.EnrichStatusSucceeded},
			{name: "default preserves instance fields", instanceFields: true, wantSet: "30", wantModule: "40", wantStatus: domain.EnrichStatusSucceeded},
			{name: "default recovers business without nodes", missingBusiness: true, wantCalls: 1, wantSet: "null", wantModule: "null", wantStatus: domain.EnrichStatusSucceeded},
			{name: "default preserves independent fields on membership failure", missingBusiness: true, instanceFields: true, failMembership: true, wantCalls: 1, wantSet: "30", wantModule: "40", wantStatus: domain.EnrichStatusPartial},
			{name: "enabled enriches both fields", enabled: true, wantCalls: 2, wantSet: "3", wantModule: "4", wantStatus: domain.EnrichStatusSucceeded},
			{name: "enabled topology overrides instance fields", enabled: true, instanceFields: true, wantCalls: 2, wantSet: "3", wantModule: "4", wantStatus: domain.EnrichStatusSucceeded},
			{name: "enabled reports topology failure", enabled: true, failMembership: true, wantCalls: 1, wantSet: "null", wantModule: "null", wantStatus: domain.EnrichStatusPartial},
		} {
			t.Run(processor+"/"+test.name, func(t *testing.T) {
				transport := &switchTopologyTransport{t: t, failMembership: test.failMembership}
				client, err := onemodel.NewClient(onemodel.ClientConfig{Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
				source := baseCollectEventSource()
				source.Version = 1 // 同时覆盖已发布 built_in_bk 的旧 resource 分派。
				source.Enrich.CMDBSetModuleEnabled = test.enabled
				source.Enrich.Processors[1].Type = processor
				sources := (&datasources.Mock{}).Sources()
				sources.CWStrategy, sources.Model, sources.Metric = baseCollectCWStrategy{}, baseCollectModel{}, baseCollectMetric{}
				sources.OneModel = switchInstanceReader{missingBusiness: test.missingBusiness, instanceFields: test.instanceFields}
				sources.CollectTopology = client
				sources.AlarmSource = sampleAlarmSource{}
				router, err := NewRouter([]config.EventSource{source}, sources)
				if err != nil {
					t.Fatal(err)
				}
				event := baseCollectAlert("built_in_bk")
				event.Dimensions["bk_target_ip"] = domain.NewStringScalar("10.0.0.1")
				event.Dimensions["bk_target_cloud_id"] = domain.NewStringScalar("0")
				original := event.Clone()
				result, err := router.Enrich(t.Context(), enrich.Input{Event: event})
				if err != nil {
					t.Fatal(err)
				}
				payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
				if err != nil {
					t.Fatal(err)
				}
				resource := payload.Processors[1][processor]
				if result.Status != test.wantStatus || resource.Status != test.wantStatus || len(transport.paths) != test.wantCalls {
					encoded, _ := json.Marshal(payload)
					t.Fatalf("status=%s payload=%s requests=%v", result.Status, encoded, transport.paths)
				}
				for key, want := range map[string]string{
					"bk_set_id": test.wantSet, "bk_module_id": test.wantModule,
					"bk_biz_id": "2", "bk_biz_name": `"业务 2"`,
					"bk_host_id": "101", "bk_host_name": `"host-101"`,
					"model_id": `"cw-Host"`, "model_inst_id": `"101"`, "bk_cloud_name": `"默认区域"`,
				} {
					if got := string(resource.Value[key]); got != want && (want != "null" || got != "") {
						t.Errorf("%s=%s, want %s", key, resource.Value[key], want)
					}
				}
				setName, moduleName := `""`, `""`
				if test.enabled && !test.failMembership {
					setName, moduleName = `"拓扑集群"`, `"拓扑模块"`
				} else if test.instanceFields {
					setName, moduleName = `"实例集群"`, `"实例模块"`
				}
				if string(resource.Value["bk_set_name"]) != setName || string(resource.Value["bk_module_name"]) != moduleName {
					t.Fatalf("names=%s/%s", resource.Value["bk_set_name"], resource.Value["bk_module_name"])
				}
				if strings.Contains(string(resource.Value["cw_labels"]), "bk_set_id|") != (test.wantSet != "null") {
					t.Fatalf("labels=%s", resource.Value["cw_labels"])
				}
				if !reflect.DeepEqual(original, event) {
					t.Fatal("original event changed")
				}
			})
		}
	}
}

type switchInstanceReader struct {
	missingBusiness bool
	instanceFields  bool
}

func (r switchInstanceReader) FindInstance(ctx context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if query.ModelCode == "cw-biz" && query.InstanceID == "2" {
		return enrich.Instance{TenantID: tenant, ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{"bk_biz_name": "业务 2"}}, true, nil
	}
	instance, found, err := (baseCollectOneModel{}).FindInstance(ctx, tenant, query)
	if found {
		if r.missingBusiness {
			delete(instance.Fields, "bk_biz_ids")
			delete(instance.Attributes, "bk_biz_id")
			delete(instance.Attributes, "bk_biz_name")
		}
		if r.instanceFields {
			instance.Attributes["bk_set_id"], instance.Attributes["bk_set_name"] = 30, "实例集群"
			instance.Attributes["bk_module_id"], instance.Attributes["bk_module_name"] = 40, "实例模块"
		}
	}
	return instance, found, err
}

type switchTopologyTransport struct {
	t              *testing.T
	failMembership bool
	paths          []string
}

func (s *switchTopologyTransport) Perform(request *http.Request) (*http.Response, error) {
	s.paths = append(s.paths, request.URL.Path)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(body), datasources.SampleTenantID) {
		s.t.Fatal("topology query missing tenant")
	}
	var rows []map[string]any
	switch request.URL.Path {
	case "/bk_monitor_base_cmdb_biz_topo_host_membership/_search":
		if s.failMembership {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
		}
		rows = []map[string]any{{"bk_tenant_id": datasources.SampleTenantID, "bk_biz_id": 2,
			"model_id": rules.HostModelCode, "model_inst_id": "101", "entity_uid": "cw-Host|101",
			"topology_ancestor_unique_ids": []string{"biz", "set", "module"}}}
	case "/bk_monitor_base_cmdb_biz_topo_node/_search":
		for _, node := range []struct{ uid, model, id, name, object string }{
			{"biz", "cw-biz", "2", "业务 2", "biz"}, {"set", "cw-Set", "3", "拓扑集群", "set"}, {"module", "cw-Module", "4", "拓扑模块", "module"},
		} {
			rows = append(rows, map[string]any{"bk_tenant_id": datasources.SampleTenantID, "bk_biz_id": 2,
				"unique_id": node.uid, "model_id": node.model, "model_inst_id": node.id, "bk_inst_name": node.name, "bk_obj_id": node.object})
		}
	default:
		s.t.Fatalf("unexpected query: %s", request.URL.Path)
	}
	hits := make([]map[string]any, len(rows))
	for i, row := range rows {
		hits[i] = map[string]any{"_source": row}
	}
	encoded, err := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(encoded))}, err
}

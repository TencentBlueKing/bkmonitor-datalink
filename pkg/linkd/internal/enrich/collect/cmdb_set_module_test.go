package collect

import (
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestCollectCMDBSetModuleDisabled(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, knownBusiness := range []bool{false, true} {
			t.Run(testName(remote, knownBusiness), func(t *testing.T) {
				host := enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101",
					Fields: map[string]any{"bk_host_id": int64(101), "bk_cloud_id": int64(0), "bk_cloud_name": "默认区域"}}
				if knownBusiness {
					host.Fields["bk_biz_id"], host.Fields["bk_biz_name"] = int64(3), "业务 3"
				}
				reader := &collectReader{
					config: models.CollectConfig{UID: "uid", BKTenantID: "tenant-a", BKCollectTaskID: "collect", BKObjectCode: rules.HostModelCode, BKInstID: 101}, configFound: true,
					instance: host, instanceFound: true,
					topology: models.ResourceTopology{BKBizID: 3, BKSetID: 5, BKModuleID: 6}, topologyFound: true,
				}
				if remote {
					reader.config.BKObjectCode, reader.config.BKInstID, reader.config.IsRemoteCollect = "cw-Service", 2, true
					reader.model = enrich.Model{TenantID: "tenant-a", ModelID: "20", ModelCode: "cw-Service", Fields: map[string]any{"host_related_field": "run_host"}}
					reader.modelFound = true
					reader.relatedHost, reader.relatedFound = host, true
					reader.instance = enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2", Fields: map[string]any{}}
				}
				// 两种采集均保留实例自带集群、模块；关联主机的拓扑不能覆盖它们。
				reader.instance.Fields["bk_set_id"], reader.instance.Fields["bk_set_name"] = int64(30), "实例集群"
				reader.instance.Fields["bk_module_id"], reader.instance.Fields["bk_module_name"] = int64(40), "实例模块"
				event := collectAlert(t, domain.DimensionMap{"bk_collect_config_id": domain.NewStringScalar("collect")})
				scope, err := enrich.NewScope(event, enrich.Sources{CollectConfig: reader, Model: reader, OneModel: reader, CollectTopology: reader})
				if err != nil {
					t.Fatal(err)
				}
				result, err := Enrich(t.Context(), scope, event.Dimensions, 2, event.SubjectName)
				if err != nil {
					t.Fatal(err)
				}
				if len(result.ResourceDiagnostics) != 0 || result.Resource.BKBizID != int64(3) || result.Resource.BKCloudName != "默认区域" ||
					result.Resource.BKSetID != int64(30) || result.Resource.BKModuleID != int64(40) ||
					result.Resource.BKSetName != "实例集群" || result.Resource.BKModuleName != "实例模块" {
					t.Fatalf("result=%#v", result)
				}
				wantCalls := 1
				if knownBusiness {
					wantCalls = 0
				}
				if reader.topologyCalls != wantCalls {
					t.Fatalf("topology calls=%d, want %d", reader.topologyCalls, wantCalls)
				}
			})
		}
	}
}

func testName(remote, knownBusiness bool) string {
	name := "host"
	if remote {
		name = "remote"
	}
	if knownBusiness {
		return name + "/known_business"
	}
	return name + "/membership_business"
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestSpaceSvc_RefreshBkccSpaceName(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		data := `{"result":true,"code":0,"data":{"count":2,"info":[{"bk_biz_developer":"","bk_biz_id":121,"bk_biz_maintainer":"admin","bk_biz_name":"蓝鲸121","bk_biz_productor":"","bk_biz_tester":"test8","bk_supplier_account":"0","create_time":"2023-05-23T23:19:57.356+08:00","db_app_abbr":"blueking","default":0,"language":"1","last_time":"2023-11-28T10:45:12.201+08:00","life_cycle":"2","operator":"","time_zone":"Asia/Shanghai"},{"bk_biz_developer":"","bk_biz_id":122,"bk_biz_maintainer":"admin","bk_biz_name":"测试业务122","bk_biz_productor":"","bk_biz_tester":"","bk_supplier_account":"0","create_time":"2023-06-09T12:05:20.042+08:00","db_app_abbr":"abbr","default":0,"language":"1","last_time":"2023-11-14T11:40:40.7+08:00","life_cycle":"2","operator":"","time_zone":"Asia/Shanghai"}]},"message":"success","permission":null,"request_id":"74cf51a3628743e194af6996389790e5"}`
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	db := mysql.GetDBSession().DB
	sp := space.Space{
		SpaceTypeId: models.SpaceTypeBKCC,
		SpaceId:     "121",
		SpaceName:   "蓝鲸_dif_name",
	}
	db.Delete(&space.Space{}, "space_id in (?)", []string{"121", "122"})
	err := sp.Create(db)
	assert.NoError(t, err)
	svc := NewSpaceSvc(nil)
	err = svc.RefreshBkccSpaceName()
	assert.NoError(t, err)
	err = space.NewSpaceQuerySet(db).SpaceIdEq("121").One(&sp)
	assert.NoError(t, err)
	assert.Equal(t, "蓝鲸121", sp.SpaceName)
}

func TestSpaceSvc_RefreshBkccSpace(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		data := `{"result":true,"code":0,"data":{"count":3,"info":[{"bk_biz_developer":"","bk_biz_id":100,"bk_biz_maintainer":"admin","bk_biz_name":"biz_100","bk_biz_productor":"","bk_biz_tester":"test8","bk_supplier_account":"0","create_time":"2023-05-23T23:19:57.356+08:00","db_app_abbr":"blueking","default":0,"language":"1","last_time":"2023-11-28T10:45:12.201+08:00","life_cycle":"2","operator":"","time_zone":"Asia/Shanghai"},{"bk_biz_developer":"","bk_biz_id":101,"bk_biz_maintainer":"admin","bk_biz_name":"biz_101","bk_biz_productor":"","bk_biz_tester":"","bk_supplier_account":"0","create_time":"2023-06-09T12:05:20.042+08:00","db_app_abbr":"abbr","default":0,"language":"1","last_time":"2023-11-14T11:40:40.7+08:00","life_cycle":"2","operator":"","time_zone":"Asia/Shanghai"},{"bk_biz_developer":"","bk_biz_id":102,"bk_biz_maintainer":"admin","bk_biz_name":"biz_102","bk_biz_productor":"","bk_biz_tester":"","bk_supplier_account":"0","create_time":"2023-06-12T14:51:21.626+08:00","db_app_abbr":"dba","default":0,"language":"1","last_time":"2023-06-12T19:52:05.248+08:00","life_cycle":"2","operator":"","time_zone":"Asia/Shanghai"}]},"message":"success","permission":null,"request_id":"a4605a2cc8ad454f8e7060f584db04ce"}`
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	redisClient := &mocker.RedisClientMocker{
		SetMap: map[string]mapset.Set[string]{},
	}
	p := gomonkey.ApplyFunc(redis.GetInstance, func() *redis.Instance {
		return &redis.Instance{
			Client: redisClient,
		}
	})
	defer p.Reset()

	spaceIds := []string{"100", "101", "102"}
	spaceUids := []string{"bkcc__100", "bkcc__101", "bkcc__102"}
	db.Delete(&space.Space{}, "space_id in (?)", spaceIds)
	svc := NewSpaceSvc(nil)
	err := svc.RefreshBkccSpace(false)
	assert.NoError(t, err)
	spaceIdSet, ok := redisClient.SetMap[models.QueryVmSpaceUidListKey]
	assert.True(t, ok)
	assert.ElementsMatch(t, spaceIdSet.ToSlice(), spaceUids)
	var sp100, sp101, sp102 space.Space
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceIdEq("100").SpaceNameEq("biz_100").SpaceTypeIdEq(models.SpaceTypeBKCC).StatusEq("normal").IsBcsValidEq(false).One(&sp100))
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceIdEq("101").SpaceNameEq("biz_101").SpaceTypeIdEq(models.SpaceTypeBKCC).StatusEq("normal").IsBcsValidEq(false).One(&sp101))
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceIdEq("102").SpaceNameEq("biz_102").SpaceTypeIdEq(models.SpaceTypeBKCC).StatusEq("normal").IsBcsValidEq(false).One(&sp102))

}

func TestSpaceSvc_RefreshBcsProjectBiz(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	sp1 := space.Space{
		SpaceTypeId: models.SpaceTypeBKCI,
		SpaceId:     "project_code_1",
		SpaceName:   "project_name_1",
		SpaceCode:   "code_1",
		IsBcsValid:  true,
	}
	sp2 := space.Space{
		SpaceTypeId: models.SpaceTypeBKCI,
		SpaceId:     "project_code_2",
		SpaceName:   "project_name_2",
		SpaceCode:   "code_2",
		IsBcsValid:  true,
	}
	db.Delete(&space.Space{}, "space_type_id = 'bkci' and space_id in (?)", []string{"project_code_1", "project_code_2"})
	err := sp1.Create(db)
	assert.NoError(t, err)
	err = sp2.Create(db)
	assert.NoError(t, err)

	db.Delete(&space.SpaceResource{}, "space_id in (?)", []string{"project_code_1", "project_code_2"})
	bizId := "999"
	sr := space.SpaceResource{
		SpaceTypeId:     models.SpaceTypeBKCI,
		SpaceId:         "project_code_2",
		ResourceType:    models.SpaceTypeBKCC,
		ResourceId:      &bizId,
		DimensionValues: "{}",
		BaseModel:       models.BaseModel{},
	}
	db.Delete(&sr, "space_type_id = 'bkci' and resource_type = 'bkcc' and space_id in (?)", []string{"project_code_1", "project_code_2"})
	err = sr.Create(db)
	assert.NoError(t, err)
	spaceSvcTarget := SpaceSvc{}
	patch := gomonkey.ApplyMethod(&spaceSvcTarget, "GetValidBcsProjects", func() ([]map[string]string, error) {
		return []map[string]string{
			{
				"projectId":   "project_id_1",
				"name":        "project_name_1",
				"projectCode": "project_code_1",
				"bkBizId":     "31",
			},
			{
				"projectId":   "project_id_2",
				"name":        "project_name_2",
				"projectCode": "project_code_2",
				"bkBizId":     "32",
			},
		}, nil
	})
	defer patch.Reset()
	svc := NewSpaceSvc(nil)
	err = svc.RefreshBcsProjectBiz()
	assert.NoError(t, err)
	var sr1, sr2 space.SpaceResource
	err = space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq("project_code_1").ResourceTypeEq(models.SpaceTypeBKCC).ResourceIdEq("31").One(&sr1)
	assert.NoError(t, err)
	err = space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq("project_code_2").ResourceTypeEq(models.SpaceTypeBKCC).ResourceIdEq("32").One(&sr2)
	assert.NoError(t, err)

	dm1 := []map[string]interface{}{{"bk_biz_id": "31"}}
	sr1Dm, err := sr1.GetDimensionValues()
	assert.NoError(t, err)
	equal, err := jsonx.CompareObjects(dm1, sr1Dm)
	assert.NoError(t, err)
	assert.True(t, equal)

	dm2 := []map[string]interface{}{{"bk_biz_id": "32"}}
	sr2Dm, err := sr2.GetDimensionValues()
	assert.NoError(t, err)
	equal, err = jsonx.CompareObjects(dm2, sr2Dm)
	assert.NoError(t, err)
	assert.True(t, equal)

}

func TestSpaceSvc_SyncBcsSpace(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	sp := space.Space{
		SpaceTypeId: models.SpaceTypeBKCI,
		SpaceId:     "project_code_11",
		IsBcsValid:  true,
	}
	db.Delete(&space.Space{}, "space_id in (?)", []string{"project_code_11", "project_code_22"})
	assert.NoError(t, sp.Create(db))

	db.Delete(&space.SpaceResource{}, "space_id = ?", "project_code_22")
	db.Delete(&space.SpaceDataSource{}, "space_id = ?", "project_code_22")

	clusterSingle := bcs.BCSClusterInfo{
		ClusterID:          "bcs_space_test_cluster_single",
		K8sMetricDataID:    176001,
		CustomMetricDataID: 176002,
	}
	clusterShared := bcs.BCSClusterInfo{
		ClusterID:          "bcs_space_test_cluster_shared",
		K8sMetricDataID:    176011,
		CustomMetricDataID: 176012,
	}
	db.Delete(&bcs.BCSClusterInfo{}, "cluster_id in (?)", []string{"bcs_space_test_cluster_single", "bcs_space_test_cluster_shared"})
	assert.NoError(t, clusterSingle.Create(db))
	assert.NoError(t, clusterShared.Create(db))

	ds1 := resulttable.DataSource{
		BkDataId:        176001,
		DataName:        "ds_176001",
		DataDescription: "ds_176001",
		EtlConfig:       models.ETLConfigTypeBkStandardV2TimeSeries,
		IsEnable:        true,
	}
	ds2 := resulttable.DataSource{
		BkDataId:        176002,
		DataName:        "ds_176002",
		DataDescription: "ds_176002",
		EtlConfig:       models.ETLConfigTypeBkStandardV2TimeSeries,
		IsEnable:        true,
	}
	ds3 := resulttable.DataSource{
		BkDataId:        176011,
		DataName:        "ds_176011",
		DataDescription: "ds_176011",
		EtlConfig:       models.ETLConfigTypeBkStandardV2TimeSeries,
		IsEnable:        true,
	}
	ds4 := resulttable.DataSource{
		BkDataId:        176012,
		DataName:        "ds_176012",
		DataDescription: "ds_176012",
		EtlConfig:       models.ETLConfigTypeBkStandardV2TimeSeries,
		IsEnable:        true,
	}
	db.Delete(&resulttable.DataSource{}, "bk_data_id in (?)", []uint{176001, 176002, 176011, 176012})
	assert.NoError(t, ds1.Create(db))
	assert.NoError(t, ds2.Create(db))
	assert.NoError(t, ds3.Create(db))
	assert.NoError(t, ds4.Create(db))

	spaceSvcTarget := SpaceSvc{}
	patch := gomonkey.ApplyMethod(&spaceSvcTarget, "GetValidBcsProjects", func() ([]map[string]string, error) {
		return []map[string]string{
			{
				"projectId":   "project_id_11",
				"name":        "project_name_11",
				"projectCode": "project_code_11",
				"bkBizId":     "41",
			},
			{
				"projectId":   "project_id_22",
				"name":        "project_name_22",
				"projectCode": "project_code_22",
				"bkBizId":     "42",
			},
		}, nil
	})
	defer patch.Reset()
	gomonkey.ApplyFunc(apiservice.BcsClusterManagerService.GetProjectClusters, func(s apiservice.BcsClusterManagerService, projectId string, excludeSharedCluster bool) ([]map[string]interface{}, error) {
		if projectId != "project_id_22" {
			return nil, nil
		}
		return []map[string]interface{}{
			{
				"projectId": "project_id_22",
				"clusterId": "bcs_space_test_cluster_single",
				"bkBizId":   "42",
				"isShared":  false,
			},
			{
				"projectId": "project_id_22",
				"clusterId": "bcs_space_test_cluster_shared",
				"bkBizId":   "42",
				"isShared":  true,
			},
		}, nil
	})
	gomonkey.ApplyFunc(apiservice.BcsService.FetchSharedClusterNamespaces, func(s apiservice.BcsService, clusterId string, projectCode string) ([]map[string]string, error) {
		if projectCode != "project_code_22" {
			return nil, nil
		}
		return []map[string]string{
			{
				"projectId":   "project_id_22",
				"projectCode": projectCode,
				"clusterId":   clusterId,
				"namespace":   "n1",
			},
			{
				"projectId":   "project_id_22",
				"projectCode": projectCode,
				"clusterId":   clusterId,
				"namespace":   "n2",
			},
		}, nil
	})
	svc := NewSpaceSvc(nil)
	assert.NoError(t, svc.SyncBcsSpace())
	// 已存在的space更新
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceIdEq(sp.SpaceId).One(&sp))
	assert.Equal(t, "project_name_11", sp.SpaceName)
	assert.Equal(t, "project_id_11", sp.SpaceCode)

	// 新创建的space
	var sp22 space.Space
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq("project_code_22").SpaceNameEq("project_name_22").SpaceCodeEq("project_id_22").IsBcsValidEq(true).One(&sp22))
	// spaceDataSource
	count, err := space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq("project_code_22").BkDataIdIn(176001, 176002, 176011, 176012).FromAuthorizationEq(false).Count()
	assert.NoError(t, err)
	assert.Equal(t, 4, count)
	// spaceResource
	var srBkcc, srBcs space.SpaceResource
	assert.NoError(t, space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq("project_code_22").ResourceTypeEq(models.SpaceTypeBKCC).ResourceIdEq("42").One(&srBkcc))
	assert.NoError(t, space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq("project_code_22").ResourceTypeEq(models.SpaceTypeBCS).ResourceIdEq("project_code_22").One(&srBcs))
	equal, err := jsonx.CompareJson(`[{"bk_biz_id":"42"}]`, srBkcc.DimensionValues)
	assert.NoError(t, err)
	assert.True(t, equal)
	equal, err = jsonx.CompareJson(`[{"cluster_id":"bcs_space_test_cluster_single","cluster_type":"single","namespace":null},{"cluster_id":"bcs_space_test_cluster_shared","cluster_type":"shared","namespace":["n1","n2"]}]`, srBcs.DimensionValues)
	assert.NoError(t, err)
	assert.True(t, equal)
}

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
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
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

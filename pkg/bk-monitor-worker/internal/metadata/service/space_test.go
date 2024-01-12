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
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
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
	p := gomonkey.ApplyFunc(redis.GetInstance, func(ctx context.Context) *redis.Instance {
		return &redis.Instance{
			Client: redisClient,
		}
	})
	defer p.Reset()

	spaceIds := []string{"100", "101", "102"}
	spaceUids := []string{"bkcc__100", "bkcc__101", "bkcc__102"}
	db.Delete(&space.Space{}, "space_id in (?)", spaceIds)
	svc := NewSpaceSvc(nil)
	err := svc.RefreshBkccSpace()
	assert.NoError(t, err)
	spaceIdSet, ok := redisClient.SetMap[models.QueryVmSpaceUidListKey]
	assert.True(t, ok)
	assert.ElementsMatch(t, spaceIdSet.ToSlice(), spaceUids)
	var sp100, sp101, sp102 space.Space
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceIdEq("100").SpaceNameEq("biz_100").SpaceTypeIdEq(models.SpaceTypeBKCC).StatusEq("normal").IsBcsValidEq(false).One(&sp100))
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceIdEq("101").SpaceNameEq("biz_101").SpaceTypeIdEq(models.SpaceTypeBKCC).StatusEq("normal").IsBcsValidEq(false).One(&sp101))
	assert.NoError(t, space.NewSpaceQuerySet(db).SpaceIdEq("102").SpaceNameEq("biz_102").SpaceTypeIdEq(models.SpaceTypeBKCC).StatusEq("normal").IsBcsValidEq(false).One(&sp102))

}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	innerRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
	routerInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func TestRun(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

type TestSuite struct {
	suite.Suite
	ctx    context.Context
	client goRedis.UniversalClient
	router *SpaceTsDbRouter
}

func (s *TestSuite) SetupTest() {
	config.InitConfig()
	s.ctx = context.Background()
	// 初始化全局 Redis 实例
	(&(redis.Service{})).Reload(s.ctx)
	// 需要往 redis 写入样例数据
	s.client = innerRedis.Client()

	s.client.HSet(
		s.ctx,
		"bkmonitorv3:spaces:space_to_result_table",
		"bkcc__2",
		"{\"script_hhb_test.group3\":{\"filters\":[{\"bk_biz_id\":\"2\"}]},\"redis.repl\":{\"filters\":[{\"bk_biz_id\":\"2\"}]}}")
	s.client.HSet(
		s.ctx,
		"bkmonitorv3:spaces:data_label_to_result_table",
		"script_hhb_test",
		"[\"script_hhb_test.group1\",\"script_hhb_test.group2\",\"script_hhb_test.group3\",\"script_hhb_test.group4\"]")
	s.client.HSet(
		s.ctx,
		"bkmonitorv3:spaces:field_to_result_table",
		"disk_usage12",
		"[\"script_hhb_test.group3\"]")
	s.client.HSet(
		s.ctx,
		"bkmonitorv3:spaces:result_table_detail",
		"script_hhb_test.group3",
		"{\"storage_id\":2,\"cluster_name\":\"default\",\"db\":\"script_hhb_test\",\"measurement\":\"group3\",\"vm_rt\":\"\",\"tags_key\":[],\"fields\":[\"disk_usage30\",\"disk_usage8\",\"disk_usage27\",\"disk_usage4\",\"disk_usage24\",\"disk_usage11\",\"disk_usage7\",\"disk_usage5\",\"disk_usage20\",\"disk_usage25\",\"disk_usage10\",\"disk_usage6\",\"disk_usage19\",\"disk_usage18\",\"disk_usage17\",\"disk_usage15\",\"disk_usage22\",\"disk_usage28\",\"disk_usage21\",\"disk_usage26\",\"disk_usage13\",\"disk_usage14\",\"disk_usage12\",\"disk_usage23\",\"disk_usage3\",\"disk_usage16\",\"disk_usage9\"],\"measurement_type\":\"bk_exporter\",\"bcs_cluster_id\":\"\",\"data_label\":\"script_hhb_test\"}")

	router, err := SetSpaceTsDbRouter(s.ctx, "spacetsdb_test.db", "spacetsdb_test", "bkmonitorv3:spaces")
	if err != nil {
		panic(err)
	}
	s.router = router
}

func (s *TestSuite) TearDownTest() {
	s.client.Del(
		s.ctx,
		"bkmonitorv3:spaces:space_to_result_table",
		"bkmonitorv3:spaces:data_label_to_result_table",
		"bkmonitorv3:spaces:result_table_detail",
		"bkmonitorv3:spaces:field_to_result_table")
}

func (s *TestSuite) TestReloadByKey() {
	router := s.router
	err := router.ReloadAllKey(s.ctx)
	if err != nil {
		panic(err)
	}

	space := router.GetSpace(s.ctx, "bkcc__2")
	s.T().Logf("Space: %v\n", space)
	assert.Equal(s.T(), space["script_hhb_test.group3"].Filters[0]["bk_biz_id"], "2")

	rt := router.GetResultTable(s.ctx, "script_hhb_test.group3")
	s.T().Logf("ResultTable: %v\n", rt)
	assert.Equal(s.T(), rt.DB, "script_hhb_test")

	rtIds := router.GetDataLabelRelatedRts(s.ctx, "script_hhb_test")
	s.T().Logf("Rts related data-label: %v\n", rtIds)
	assert.Contains(s.T(), rtIds, "script_hhb_test.group3")

	rtIds2 := router.GetFieldRelatedRts(s.ctx, "disk_usage12")
	s.T().Logf("Rts related by fields: %v\n", rtIds2)
	assert.Equal(s.T(), rtIds2, routerInfluxdb.ResultTableList{"script_hhb_test.group3"})

	content := router.Print(s.ctx, "")
	s.T().Logf(content)
}

func (s *TestSuite) TestReloadBySpaceKey() {
	var err error
	router := s.router

	err = router.ReloadByChannel(s.ctx, "bkmonitorv3:spaces:space_to_result_table:channel", "bkcc__2")
	if err != nil {
		panic(err)
	}
	space := router.GetSpace(s.ctx, "bkcc__2")
	s.T().Logf("Space: %v\n", space)
	assert.Equal(s.T(), space["script_hhb_test.group3"].Filters[0]["bk_biz_id"], "2")
	// 验证两次读取是否可以命中缓存，缓存生效有延迟，所以这里设置一个等待时间
	space = router.GetSpace(s.ctx, "bkcc__2222")
	s.T().Logf("Space02: %v\n", space)
	time.Sleep(1 * time.Second)
	space = router.GetSpace(s.ctx, "bkcc__2222")
	s.T().Logf("Space03: %v\n", space)

	err = router.ReloadByChannel(s.ctx, "bkmonitorv3:spaces:result_table_detail:channel", "script_hhb_test.group3")
	if err != nil {
		panic(err)
	}
	rt := router.GetResultTable(s.ctx, "script_hhb_test.group3")
	s.T().Logf("ResultTable: %v\n", rt)
	assert.Equal(s.T(), rt.DB, "script_hhb_test")

	err = router.ReloadByChannel(s.ctx, "bkmonitorv3:spaces:data_label_to_result_table:channel", "script_hhb_test")
	if err != nil {
		panic(err)
	}
	rtIds := router.GetDataLabelRelatedRts(s.ctx, "script_hhb_test")
	s.T().Logf("Rts related data-label: %v\n", rtIds)
	assert.Contains(s.T(), rtIds, "script_hhb_test.group3")

	err = router.ReloadByChannel(s.ctx, "bkmonitorv3:spaces:field_to_result_table:channel", "disk_usage12")
	if err != nil {
		panic(err)
	}
	rtIds2 := router.GetFieldRelatedRts(s.ctx, "disk_usage12")
	s.T().Logf("Rts related by fields: %v\n", rtIds2)
	assert.Equal(s.T(), rtIds2, routerInfluxdb.ResultTableList{"script_hhb_test.group3"})
}

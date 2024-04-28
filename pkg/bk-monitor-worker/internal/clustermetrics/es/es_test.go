// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

func TestRun(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

type TestSuite struct {
	suite.Suite
	dbSession *mysql.DBSession
}

func (s *TestSuite) SetupTest() {
	clustermetrics.InitTestConfig()

	s.dbSession = mysql.GetDBSession()
	s.initStoreData()
}

func (s *TestSuite) clear() {
	err := storage.NewClusterInfoQuerySet(s.dbSession.DB).ClusterTypeEq(models.StorageTypeES).Delete()
	if err != nil {
		panic(err)
	}
}

func (s *TestSuite) TearDownTest() {
	s.clear()
	s.dbSession.Close()
}

func (s *TestSuite) initStoreData() {
	clusterInfo := storage.ClusterInfo{
		DomainName:   "127.0.0.1",
		Port:         9200,
		Username:     "elastic",
		Password:     "",
		ClusterType:  models.StorageTypeES,
		CustomOption: "{\"bk_biz_id\": 2}",
	}
	err := clusterInfo.Create(s.dbSession.DB)
	if err != nil {
		panic(err)
	}
}

func (s *TestSuite) TestReportESClusterMetrics() {
	ctx := context.Background()
	tConfig := task.Task{}
	err := ReportESClusterMetrics(ctx, &tConfig)
	if err != nil {
		s.T().Errorf("Fail to report es cluster metrics, %v", err)
	}
}

func (s *TestSuite) TestIndexRegex() {
	index1 := "v2_1_bkmonitor_event_1_20240219_0"
	index2 := "v2_space_1_bklog_bcs_k8s_1_bk_gse_file_r5lmh_path_20240418_2"
	bizMatch1 := targetBizRe.FindStringSubmatch(index1)
	bizMatch2 := targetBizRe.FindStringSubmatch(index2)
	targetBizId1 := ""
	if len(bizMatch1) > 0 {
		if bizMatch1[1] == "_space" {
			targetBizId1 = "-" + bizMatch1[2]
		}
		targetBizId1 = bizMatch1[2]
	}
	targetBizId2 := ""
	if len(bizMatch2) > 0 {
		if bizMatch2[1] == "_space" {
			targetBizId2 = "-" + bizMatch2[2]
		} else {
			targetBizId2 = bizMatch2[2]
		}
	}
	if targetBizId1 != "1" {
		s.T().Errorf("expected 1, targetBizId：%s", targetBizId1)
	}
	if targetBizId2 != "-1" {
		s.T().Errorf("expected -1, targetBizId：%s", targetBizId2)
	}
}

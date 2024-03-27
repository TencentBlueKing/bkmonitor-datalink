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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/stretchr/testify/suite"
	"testing"
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

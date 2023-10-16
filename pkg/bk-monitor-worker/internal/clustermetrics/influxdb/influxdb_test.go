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

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
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

func (s *TestSuite) TearDownTest() {
	s.clear()
	s.dbSession.Close()
}

func (s *TestSuite) initStoreData() {
	cluster := storage.InfluxdbClusterInfo{
		HostName:     "INFLUXDB_IP0",
		ClusterName:  "default",
		HostReadable: true,
	}
	err := cluster.Create(s.dbSession.DB)
	if err != nil {
		panic(err)
	}
	host := storage.InfluxdbHostInfo{
		HostName:        "INFLUXDB_IP0",
		DomainName:      "127.0.0.1",
		Port:            8086,
		Username:        "foo",
		Password:        "aes_str:::1234=",
		Description:     "system auto add.",
		GrpcPort:        8089,
		Protocol:        "http",
		BackupRateLimit: 40,
		ReadRateLimit:   0,
	}
	err = host.Create(s.dbSession.DB)
	if err != nil {
		panic(err)
	}
	clusterMetric := storage.ClusterMetric{
		MetricName: "influxdb.database.num_series",
		Tags:       "[\"bkm_cluster\", \"database\", \"hostname\"]",
	}
	err = clusterMetric.Create(s.dbSession.DB)
	if err != nil {
		panic(err)
	}
}

func (s *TestSuite) clear() {
	err := storage.NewInfluxdbClusterInfoQuerySet(s.dbSession.DB).Delete()
	if err != nil {
		panic(err)
	}
	err = storage.NewInfluxdbHostInfoQuerySet(s.dbSession.DB).Delete()
	if err != nil {
		panic(err)
	}
	err = storage.NewClusterMetricQuerySet(s.dbSession.DB).Delete()
	if err != nil {
		panic(err)
	}
}

func (s *TestSuite) TestReportInfluxdbClusterMetric() {
	ctx := context.Background()
	tConfig := task.Task{}

	err := ReportInfluxdbClusterMetric(ctx, &tConfig)
	if err != nil {
		s.T().Errorf("Fail to report, %v", err)
	}
}

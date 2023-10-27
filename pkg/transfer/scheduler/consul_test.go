// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// DispatchConverterSuite
type DispatchConverterSuite struct {
	suite.Suite
	source, target string
}

// SetupTest
func (s *DispatchConverterSuite) SetupTest() {
	s.source = "source"
	s.target = "target"
}

// TestElementCreator
func (s *DispatchConverterSuite) TestElementCreator() {
	cases := []struct {
		conf  config.PipelineConfig
		id    int
		count int
	}{
		{
			conf: config.PipelineConfig{
				DataID: 10086,
			},
			id:    10086,
			count: 1,
		},
		{
			conf: config.PipelineConfig{
				DataID:   10086,
				MQConfig: &config.MetaClusterInfo{},
			},
			id:    10086,
			count: 1,
		},
		{
			conf: config.PipelineConfig{
				DataID: 10086,
				MQConfig: &config.MetaClusterInfo{
					StorageConfig: map[string]interface{}{},
				},
			},
			id:    10086,
			count: 1,
		},
		{
			conf: config.PipelineConfig{
				DataID: 10086,
				MQConfig: &config.MetaClusterInfo{
					StorageConfig: map[string]interface{}{
						"partition": 2,
					},
				},
			},
			id:    10086,
			count: 2,
		},
	}

	for i, c := range cases {
		value, err := json.Marshal(&c.conf)
		s.NoError(err, i)
		pair := &consul.KVPair{
			Key:   "test",
			Value: value,
		}

		converter := scheduler.NewDispatchConverter(s.source, s.target)
		ider, err := converter.ElementCreator(pair)
		s.NoError(err, i)
		s.Equal(c.count, len(ider), i)

		for _, id := range ider {
			s.True(c.count >= id.ID()-c.id)
		}
	}
}

// TestNodeCreator
func (s *DispatchConverterSuite) TestNodeCreator() {
	cases := []struct {
		info define.ServiceInfo
		id   int
	}{
		{
			info: define.ServiceInfo{
				ID: "test",
			},
			id: 1874609437,
		},
		{
			info: define.ServiceInfo{
				ID:     "test",
				Detail: true,
			},
			id: 1874609437,
		},
		{
			info: define.ServiceInfo{
				ID:     "ylp",
				Detail: true,
			},
			id: 4121350035,
		},
	}

	for i, c := range cases {
		converter := scheduler.NewDispatchConverter(s.source, s.target)
		ider, err := converter.NodeCreator(&c.info)
		s.NoError(err, i)
		s.Equal(c.id, ider.ID(), i)
	}
}

// TestShadowCreator
func (s *DispatchConverterSuite) TestShadowCreator() {
	cases := []struct {
		serviceID string
		key       string
	}{
		{
			serviceID: "ylp",
			key:       "/x",
		},
		{
			serviceID: "ylp",
			key:       "x",
		},
	}

	for i, c := range cases {
		s.NotPanics(func() {
			converter := scheduler.NewDispatchConverter(s.source, s.target)
			info := define.ServiceInfo{
				ID: c.serviceID,
			}
			node := utils.NewDetailsBalanceElements(&info, 1)[0]

			key := s.source + c.key
			item := consul.DispatchItemConf{
				Pair: &consul.KVPair{Key: key},
			}
			element := utils.NewDetailsBalanceElements(&item, 1)[0]
			shadowed := fmt.Sprintf("%s/%s%s", s.target, c.serviceID, c.key)

			source, target, session, err := converter.ShadowCreator(node, element)
			s.NoError(err, i)
			s.Equal(key, source, i)
			s.Equal(shadowed, target, i)
			s.Equal(c.serviceID, session)
		})
	}
}

// TestShadowDetector
func (s *DispatchConverterSuite) TestShadowDetector() {
	cases := []struct {
		key     string
		source  string
		service string
	}{
		{s.target + "/service/my/path", s.source + "/my/path", "service"},
	}

	for _, c := range cases {
		converter := scheduler.NewDispatchConverter(s.source, s.target)
		source, target, service, err := converter.ShadowDetector(&consul.KVPair{
			Key: c.key,
		})
		s.NoError(err)
		s.Equal(c.source, source)
		s.Equal(c.key, target)
		s.Equal(c.service, service)
	}
}

// ClusterHelperSuite
type ClusterHelperSuite struct {
	testsuite.ConfigSuite
}

// TestUsage
func (s *ClusterHelperSuite) TestUsage() {
	_, err := scheduler.NewClusterHelper(s.CTX, s.Config)
	s.NoError(err)
}

// TestDispatchConverter
func TestDispatchConverter(t *testing.T) {
	suite.Run(t, new(DispatchConverterSuite))
}

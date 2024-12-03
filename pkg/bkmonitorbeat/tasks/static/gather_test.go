// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package static_test

import (
	"context"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/static"
)

func TestRun(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

type TestSuite struct {
	suite.Suite
	ctrl *gomock.Controller
	stub *gostub.Stubs
}

// SetupTest 组装环境
func (s *TestSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())
	s.stub = gostub.New()
}

// TearDownTest 卸载环境
func (s *TestSuite) TearDownTest() {
	s.ctrl.Finish()
	s.stub.Reset()
}

// TestGather 测试基本的采集
func (s *TestSuite) TestGather() {
	callIndex := 0
	reportList := []*static.Report{
		{
			CPU: &static.CPU{
				Total: 8,
				Model: "test model",
			},
			Memory: &static.Memory{
				Total: 12345,
			},
			Disk: &static.Disk{
				Total: 123456,
			},
			Net: &static.Net{
				Interface: []static.Interface{
					{
						Addrs: []string{"127.0.0.1", "127.0.0.2"},
						Mac:   "sshhdd::ddd",
					},
				},
			},
			System: &static.System{
				HostName: "test_host",
				OS:       "linux",
				Platform: "sg",
				PlatVer:  "1.2.3",
				SysType:  "64-bit",
			},
		},
		{
			CPU: &static.CPU{ // 调整了核数
				Total: 10,
				Model: "test model",
			},
			Memory: &static.Memory{
				Total: 12345,
			},
			Disk: &static.Disk{
				Total: 123456,
			},
			Net: &static.Net{
				Interface: []static.Interface{
					{
						Addrs: []string{"127.0.0.1", "127.0.0.2"},
						Mac:   "sshhdd::ddd",
					},
				},
			},
			System: &static.System{
				HostName: "test_host",
				OS:       "linux",
				Platform: "sg",
				PlatVer:  "1.2.3",
				SysType:  "64-bit",
			},
		},
	}
	s.stub.Stub(&static.GetData, func(ctx context.Context) (*static.Report, error) {
		if callIndex == 5 {
			return reportList[1], nil
		}
		return reportList[0], nil
	})

	// 将随机延迟时间固定为5s
	s.stub.StubFunc(&static.GetRandomDuration, 5*time.Second)
	defer s.stub.Reset()

	var globalConfig define.Config
	taskConfig := configs.NewStaticTaskConfig()
	taskConfig.CheckPeriod = 5 * time.Second
	taskConfig.ReportPeriod = 15 * time.Second
	gather := static.New(globalConfig, taskConfig)
	ch := make(chan define.Event)
	go func() {
		callIndex++
		// 第一次启动，会进行初次上报
		gather.Run(context.Background(), ch)
		callIndex++
		// 立刻启动第二次，不会上报
		gather.Run(context.Background(), ch)
		time.Sleep(5 * time.Second)
		// 启动第三次，会上报一次，是因为达到了延迟时间
		callIndex++
		gather.Run(context.Background(), ch)
		time.Sleep(15 * time.Second)
		// 启动第四次，此时也会上报一次数据,是因为达到了report周期
		callIndex++
		gather.Run(context.Background(), ch)
		time.Sleep(5 * time.Second)
		callIndex++
		// 启动第五次，此时也会上报，因为数据发生了改变，且check周期已经到了
		gather.Run(context.Background(), ch)
		// 启动第六次，此时什么周期都没到，所以不上报
		callIndex++
		gather.Run(context.Background(), ch)
		close(ch)
	}()
	count := 0
	for event := range ch {
		mapStr := event.AsMapStr()
		data := mapStr["data"].(common.MapStr)
		cpu := data["cpu"].(common.MapStr)
		if count == 3 {
			s.Equal(10, cpu["total"])
		} else {
			s.Equal(8, cpu["total"])
		}

		count++
	}
	s.Equal(4, count)
}

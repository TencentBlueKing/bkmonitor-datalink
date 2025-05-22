// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb_test

import (
	"sync/atomic"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/esb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

var limit = 500

type TestCCTaskSuite struct {
	// suite.Suite
	// Stubs *gostub.Stubs
	testsuite.ContextSuite
	client    *testsuite.MockApiClient
	apiClient CCApiClientSuite
}

func (t *TestCCTaskSuite) SetupTest() {
	ctrl := gomock.NewController(t.T())
	t.client = testsuite.NewMockApiClient(ctrl)
	t.ContextSuite.SetupTest()
	esb.MaxWorkerConfig = 50
}

func (t *TestCCTaskSuite) TestCCTask() {
	var (
		testTask = []esb.Task{
			{
				BizID: 200,
				Start: 500,
				Limit: limit,
			},
			{
				BizID: 200,
				Start: 1000,
				Limit: limit,
			},
			{
				BizID: 300,
				Start: 500,
				Limit: limit,
			},
			{
				BizID: 300,
				Start: 1000,
				Limit: limit,
			},
		}
		firstCount            int64 = 0
		firstTestCallBackFunc       = func(task esb.Task) {
			atomic.AddInt64(&firstCount, 1)
		}

		count            int64 = 0
		testCallBackFunc       = func(task esb.Task) {
			atomic.AddInt64(&count, 1)
		}
	)

	manager, err := esb.NewTaskManage(t.CTX, 50, firstTestCallBackFunc, testTask)
	t.Equal(nil, err)

	// 非任务执行时
	t.Equal(nil, manager.Start())
	t.Equal(nil, manager.Stop())
	t.Equal(nil, manager.Wait())
	t.NotEqual(4, count)
	t.NoError(manager.Stop())

	// 重新执行任务
	count = 0
	manager, err = esb.NewTaskManage(t.CTX, 50, testCallBackFunc, testTask)
	t.Equal(nil, err)
	t.Equal(nil, manager.Start())
	t.Equal(nil, manager.WaitJob())
	t.Equal(nil, manager.Wait())
	t.Equal(int64(len(testTask)), count)
}

func (t *TestCCTaskSuite) TestGetAllTaskInfo() {
	var (
		resultTaskList []esb.Task
		err            error
	)
	loadStore := func(monitorInfo esb.CCSearchHostResponseDataV3Monitor, info models.CCInfo) error {
		t.Equal([]esb.CCSearchHostResponseDataV3Monitor{
			{
				Info: []esb.CCSearchHostResponseInfoV3Topo{
					{
						Topo: []map[string]string{
							{
								"test": "21",
							},
							{
								"test2": "221",
							},
						},
					},
				},
			},
		}[0].Info[0].Topo[0]["test"], monitorInfo.Info[0].Topo[0]["test"])
		return nil
	}
	t.client.EXPECT().GetHostsByRange(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&esb.CCSearchHostResponseData{
		Count: 100,
		Info: []esb.CCSearchHostResponseInfo{
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.1",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 1,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
		},
	}, nil).AnyTimes()
	t.client.EXPECT().GetSearchBizInstTopo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]esb.CCSearchBizInstTopoResponseInfo{
		{
			Inst:      12,
			InstName:  "moduleTest",
			BkObjID:   define.RecordBkModuleID,
			BkObjName: "",
			Child: []*esb.CCSearchBizInstTopoResponseInfo{
				{
					Inst:      21,
					InstName:  "moduleTest",
					BkObjID:   "test",
					BkObjName: "",
				},
			},
		}, {
			Inst:      2,
			InstName:  "setTest",
			BkObjID:   "set",
			BkObjName: "",
			Child: []*esb.CCSearchBizInstTopoResponseInfo{
				{
					Inst:      21,
					InstName:  "moduleTest",
					BkObjID:   "test",
					BkObjName: "",
				},
			},
		},
	}, nil).AnyTimes()
	t.client.EXPECT().GetSearchBusiness().Return([]esb.CCSearchBusinessResponseInfo{
		{BKBizID: 1, BKBizName: "BKBizID"},
		{BKBizID: 2, BKBizName: "BKBizID"},
		{BKBizID: 3, BKBizName: "BKBizID"},
		{BKBizID: 4, BKBizName: "BKBizID"},
		{BKBizID: 5, BKBizName: "BKBizID"},
		{BKBizID: 6, BKBizName: "BKBizID"},
	}, nil).AnyTimes()

	// resultTaskList, err = esb.GetAllTaskInfo(t.client, limit, loadStore, nil)

	resultTaskList, err = esb.GetAllTaskInfo(t.client, 1, nil, loadStore)
	t.Equal(nil, err)

	t.Equal(0, len(resultTaskList))
}

func (t *TestCCTaskSuite) TestGetAllTaskInfoMultiTask() {
	t.client.EXPECT().GetSearchBusiness().Return([]esb.CCSearchBusinessResponseInfo{
		{BKBizID: 1, BKBizName: "BKBizID"},
		{BKBizID: 2, BKBizName: "BKBizID"},
		{BKBizID: 3, BKBizName: "BKBizID"},
		{BKBizID: 4, BKBizName: "BKBizID"},
		{BKBizID: 5, BKBizName: "BKBizID"},
		{BKBizID: 6, BKBizName: "BKBizID"},
	}, nil).AnyTimes()
	t.client.EXPECT().GetSearchBizInstTopo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]esb.CCSearchBizInstTopoResponseInfo{
		{
			Inst:      12,
			InstName:  "moduleTest",
			BkObjID:   define.RecordBkModuleID,
			BkObjName: "",
			Child: []*esb.CCSearchBizInstTopoResponseInfo{
				{
					Inst:      21,
					InstName:  "moduleTest",
					BkObjID:   "test",
					BkObjName: "",
				},
			},
		}, {
			Inst:      2,
			InstName:  "setTest",
			BkObjID:   define.RecordBkSetID,
			BkObjName: "",
			Child: []*esb.CCSearchBizInstTopoResponseInfo{
				{
					Inst:      21,
					InstName:  "moduleTest",
					BkObjID:   "test",
					BkObjName: "",
				},
			},
		},
	}, nil).AnyTimes()
	t.client.EXPECT().GetHostsByRange(gomock.Any(), 1, gomock.Any(), gomock.Any()).Return(&esb.CCSearchHostResponseData{
		Count: 100,
		Info: []esb.CCSearchHostResponseInfo{
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.1",
					BKOuterIP:     "8.8.8.1",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 11,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.2",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 12,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.3",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 13,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.4",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 14,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.5",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 15,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.1",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 16,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 126,
							},
						},
					},
				},
			},
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.7",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 17,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
			{
				Host: esb.CCSearchHostResponseHostInfo{
					BKCloudID:     1,
					BKHostInnerIP: "127.0.0.8",
					BKOuterIP:     "8.8.8.8",
				},
				Topo: []esb.HostTopoV3{
					{
						BKSetID: 18,
						Module: []esb.CCHostSearchModule{
							{
								BKModuleID: 12,
							},
						},
					},
				},
			},
		},
	}, nil).AnyTimes()
	loadStore := func(monitorInfo esb.CCSearchHostResponseDataV3Monitor, info models.CCInfo) error {
		t.Equal([]esb.CCSearchHostResponseDataV3Monitor{
			{
				Info: []esb.CCSearchHostResponseInfoV3Topo{
					{
						Topo: []map[string]string{
							{
								"test": "21",
							},
							{
								"test2": "221",
							},
						},
					},
				},
			},
		}[0].Info[0].Topo[0]["test"], monitorInfo.Info[0].Topo[0]["test"])
		return nil
	}
	_, err := esb.GetAllTaskInfo(t.client, 1, nil, loadStore)
	t.NoError(err)
}

// TestCCApiClientSuite :
func TestCCUtilsSuite(t *testing.T) {
	suite.Run(t, new(TestCCTaskSuite))
}

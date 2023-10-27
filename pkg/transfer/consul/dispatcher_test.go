// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// DispatcherSuite :
type DispatcherSuite struct {
	LeaderMixinSuite
	kvAPI          *MockKvAPI
	dispatcher     *consul.Dispatcher
	converter      *MockDispatchConverter
	targetRoot     string
	manualRoot     string
	trigger        *testsuite.MockTask
	triggerCreator func(i context.Context, ch chan *consul.DispatchItem) define.Task
}

// SetupTest :
func (s *DispatcherSuite) SetupTest() {
	s.LeaderMixinSuite.SetupTest()
	s.converter = NewMockDispatchConverter(s.Ctrl)
	s.kvAPI = NewMockKvAPI(s.Ctrl)
	s.client.EXPECT().KV().Return(s.kvAPI).AnyTimes()

	s.targetRoot = "/"
	s.manualRoot = "/manual"
	s.trigger = testsuite.NewMockTask(s.Ctrl)
	s.dispatcher = consul.NewDispatcher(consul.DispatcherConfig{
		Context:    s.CTX,
		Converter:  s.converter,
		Client:     s.client,
		TargetRoot: s.targetRoot,
		ManualRoot: s.manualRoot,
		TriggerCreator: func(i context.Context, items chan *consul.DispatchItem) define.Task {
			return s.trigger
		},
		DispatchDelay:   time.Second,
		RecoverInterval: time.Second,
	})
}

// MakeShadowPair :
func (s *DispatcherSuite) MakeShadowPair(source, target string, version uint64) *consul.KVPair {
	pair, err := consul.GetShadowBySourcePair(target, &consul.KVPair{
		Key:         source,
		ModifyIndex: version,
		CreateIndex: 0,
	})
	s.NoError(err)
	return pair
}

// MakePairDispatchInfo :
func (s *DispatcherSuite) MakePairDispatchInfo(source, target string, version uint64) *define.PairDispatchInfo {
	return &define.PairDispatchInfo{
		Source:  source,
		Target:  target,
		Version: version,
	}
}

// SetupShadowDetector :
func (s *DispatcherSuite) SetupShadowDetector() {
	s.converter.EXPECT().ShadowDetector(gomock.Any()).DoAndReturn(func(pair *consul.KVPair) (source, target, service string, err error) {
		parts := strings.Split(pair.Key, "/")
		return parts[1], pair.Key, parts[0], nil
	}).AnyTimes()
}

// TestDispatchRecover :
func (s *DispatcherSuite) TestDispatchRecover() {
	cases := []struct {
		service, source, target string
	}{
		{"a", "1", "a/1"},
		{"b", "2", "b/2"},
		{"c", "3", "c/3"},
	}

	s.kvAPI.EXPECT().List(s.targetRoot, gomock.Any()).Return(consul.KVPairs{
		s.MakeShadowPair(cases[0].source, cases[0].target, 0),
		s.MakeShadowPair(cases[1].source, cases[1].target, 1),
		s.MakeShadowPair(cases[2].source, cases[2].target, 2),
	}, nil, nil)
	s.SetupShadowDetector()

	s.NoError(s.dispatcher.Recover())
	visited := 0
	s.dispatcher.VisitPlan(func(service *define.ServiceDispatchInfo, pair *define.PairDispatchInfo) bool {
		visited++
		info := cases[pair.Version]
		s.Equal(info.source, pair.Source)
		s.Equal(info.target, pair.Target)
		s.Equal(info.service, service.Service)
		return true
	})
	s.Equal(len(cases), visited)
}

// SetupElementCreator :
func (s *DispatcherSuite) SetupElementCreator() {
	s.converter.EXPECT().ElementCreator(gomock.Any()).DoAndReturn(func(element *consul.KVPair) ([]define.IDer, error) {
		count, err := strconv.Atoi("0" + string(element.Value))
		s.NoError(err)
		if count == 0 {
			count = 1
		}
		return utils.NewDetailsBalanceElementsWithID(int(element.CreateIndex), element, count), nil
	}).AnyTimes()
}

// SetupNodeCreator :
func (s *DispatcherSuite) SetupNodeCreator() {
	s.converter.EXPECT().NodeCreator(gomock.Any()).DoAndReturn(func(node *define.ServiceInfo) (define.IDer, error) {
		return utils.NewDetailsBalanceElementsWithID(node.Port, node, 1)[0], nil
	}).AnyTimes()
}

// SetupShadowCreator :
func (s *DispatcherSuite) SetupShadowCreator() {
	s.converter.EXPECT().ShadowCreator(gomock.Any(), gomock.Any()).DoAndReturn(func(node define.IDer, element define.IDer) (string, string, string, error) {
		pair := element.(*utils.DetailsBalanceElement).Details.(*consul.KVPair)
		info := node.(*utils.DetailsBalanceElement).Details.(*define.ServiceInfo)
		return pair.Key, fmt.Sprintf("%s/%s", info.ID, pair.Key), info.ID, nil
	}).AnyTimes()
}

func (s *DispatcherSuite) CreateManualKV(target, dataid string) *consul.KVPair {
	return &consul.KVPair{
		Key:   path.Join(s.dispatcher.ManualRoot, dataid),
		Value: []byte(fmt.Sprintf("[{\"name\":\"%s\"}]", target)),
	}
}

func (s *DispatcherSuite) TestManualPlan() {
	metadataRoot := "/metadata"
	s.kvAPI.EXPECT().List(s.dispatcher.ManualRoot+"/", gomock.Any()).Return(consul.KVPairs{
		s.CreateManualKV("transfer1", "1002"),
		s.CreateManualKV("transfer3", "1005"),
		s.CreateManualKV("transfer3", "1001"),
	}, nil, nil).Times(1)

	preTransfer1 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1001"): {path.Join(metadataRoot, "1001"), path.Join(s.dispatcher.TargetRoot, "transfer1", "1001"), 1},
		path.Join(metadataRoot, "1004"): {path.Join(metadataRoot, "1004"), path.Join(s.dispatcher.TargetRoot, "transfer1", "1004"), 4},
	}
	preTransfer2 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1002"): {path.Join(metadataRoot, "1002"), path.Join(s.dispatcher.TargetRoot, "transfer2", "1002"), 2},
		path.Join(metadataRoot, "1005"): {path.Join(metadataRoot, "1005"), path.Join(s.dispatcher.TargetRoot, "transfer2", "1005"), 5},
	}
	preTransfer3 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1003"): {path.Join(metadataRoot, "1003"), path.Join(s.dispatcher.TargetRoot, "transfer3", "1003"), 3},
	}

	afterTransfer1 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1002"): {path.Join(metadataRoot, "1002"), path.Join(s.dispatcher.TargetRoot, "transfer1", "1002"), 2},
		path.Join(metadataRoot, "1004"): {path.Join(metadataRoot, "1004"), path.Join(s.dispatcher.TargetRoot, "transfer1", "1004"), 4},
	}
	afterTransfer2 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1003"): {path.Join(metadataRoot, "1003"), path.Join(s.dispatcher.TargetRoot, "transfer2", "1003"), 3},
	}
	afterTransfer3 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1005"): {path.Join(metadataRoot, "1005"), path.Join(s.dispatcher.TargetRoot, "transfer3", "1005"), 5},
		path.Join(metadataRoot, "1001"): {path.Join(metadataRoot, "1001"), path.Join(s.dispatcher.TargetRoot, "transfer3", "1001"), 1},
	}

	services := []*define.ServiceInfo{
		{ID: "transfer1"},
		{ID: "transfer2"},
		{ID: "transfer3"},
	}

	plans := map[string]*define.ServiceDispatchPlan{
		"transfer1": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer1"}, Pairs: preTransfer1},
		"transfer2": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer2"}, Pairs: preTransfer2},
		"transfer3": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer3"}, Pairs: preTransfer3},
	}

	expected := map[string]*define.ServiceDispatchPlan{
		"transfer1": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer1"}, Pairs: afterTransfer1},
		"transfer2": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer2"}, Pairs: afterTransfer2},
		"transfer3": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer3"}, Pairs: afterTransfer3},
	}

	err := s.dispatcher.MakeManualPlan(plans, services)

	s.Nil(err)
	for service, plan := range expected {
		resultPlan := plans[service]
		s.NotNil(resultPlan)
		for key, pair := range plan.Pairs {
			s.NotNil(resultPlan.Pairs[key])
			s.Equal(resultPlan.Pairs[key].Source, pair.Source)
			s.Equal(resultPlan.Pairs[key].Target, pair.Target)
			s.Equal(resultPlan.Pairs[key].Version, pair.Version)
		}
	}
}

// 测试手动分配的data_id是存在重复的情况
func (s *DispatcherSuite) TestManualPlanDuplicate() {
	metadataRoot := "/metadata"
	s.kvAPI.EXPECT().List(s.dispatcher.ManualRoot+"/", gomock.Any()).Return(consul.KVPairs{
		s.CreateManualKV("transfer2", "1002"),
	}, nil, nil).Times(1)

	preTransfer1 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1001"): {path.Join(metadataRoot, "1001"), path.Join(s.dispatcher.TargetRoot, "transfer1", "1001"), 1},
		path.Join(metadataRoot, "1002"): {path.Join(metadataRoot, "1002"), path.Join(s.dispatcher.TargetRoot, "transfer1", "1002"), 4},
	}
	preTransfer2 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1001"): {path.Join(metadataRoot, "1001"), path.Join(s.dispatcher.TargetRoot, "transfer2", "1001"), 1},
	}

	afterTransfer1 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1001"): {path.Join(metadataRoot, "1001"), path.Join(s.dispatcher.TargetRoot, "transfer1", "1001"), 1},
	}
	afterTransfer2 := map[string]*define.PairDispatchInfo{
		path.Join(metadataRoot, "1001"): {path.Join(metadataRoot, "1001"), path.Join(s.dispatcher.TargetRoot, "transfer2", "1001"), 1},
		path.Join(metadataRoot, "1002"): {path.Join(metadataRoot, "1002"), path.Join(s.dispatcher.TargetRoot, "transfer2", "1002"), 4},
	}

	services := []*define.ServiceInfo{
		{ID: "transfer1"},
		{ID: "transfer2"},
	}

	plans := map[string]*define.ServiceDispatchPlan{
		"transfer1": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer1"}, Pairs: preTransfer1},
		"transfer2": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer2"}, Pairs: preTransfer2},
	}

	expected := map[string]*define.ServiceDispatchPlan{
		"transfer1": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer1"}, Pairs: afterTransfer1},
		"transfer2": {ServiceDispatchInfo: &define.ServiceDispatchInfo{Service: "transfer2"}, Pairs: afterTransfer2},
	}

	err := s.dispatcher.MakeManualPlan(plans, services)

	s.Nil(err)
	for service, plan := range expected {
		resultPlan := plans[service]
		s.NotNil(resultPlan)
		for key, pair := range plan.Pairs {
			s.NotNil(resultPlan.Pairs[key])
			s.Equal(resultPlan.Pairs[key].Source, pair.Source)
			s.Equal(resultPlan.Pairs[key].Target, pair.Target)
			s.Equal(resultPlan.Pairs[key].Version, pair.Version)
		}
	}
}

// TestPlan :
func (s *DispatcherSuite) TestPlan() {
	s.SetupElementCreator()
	s.SetupNodeCreator()
	s.SetupShadowCreator()

	excepted := map[string]map[string]*define.PairDispatchInfo{
		"a": {
			"1": s.MakePairDispatchInfo("1", "a/1", 4),
		},
		"b": {
			"2": s.MakePairDispatchInfo("2", "b/2", 3),
		},
	}

	pairs := make(consul.KVPairs, 0)
	services := make([]*define.ServiceInfo, 0)
	port := 1
	index := 1
	for key, mappings := range excepted {
		port++
		services = append(services, &define.ServiceInfo{
			ID:   key,
			Port: port,
		})
		for _, info := range mappings {
			index++
			pairs = append(pairs, &consul.KVPair{
				Key:         info.Source,
				CreateIndex: uint64(index),
				ModifyIndex: info.Version,
			})
		}
	}

	plan, _ := s.dispatcher.Plan(pairs, services)
	visited := 0
	for _, service := range plan.Plans {
		info := excepted[service.Service]
		for key, pair := range service.Pairs {
			visited++
			s.Equal(info[key].Source, pair.Source)
			s.Equal(info[key].Target, pair.Target)
			s.Equal(info[key].Version, pair.Version)
		}
	}
	s.Equal(2, visited)
}

// TestDispatch :
func (s *DispatcherSuite) TestDispatch() {
	s.SetupShadowDetector()
	s.SetupElementCreator()
	s.SetupNodeCreator()
	s.SetupShadowCreator()
	s.kvAPI.EXPECT().List(s.dispatcher.ManualRoot+"/", gomock.Any()).Return(consul.KVPairs{}, nil, nil).AnyTimes()
	s.kvAPI.EXPECT().List(s.targetRoot, gomock.Any()).Return(consul.KVPairs{
		// 不变
		s.MakeShadowPair("1", "a/1", 1),
		// 改变
		s.MakeShadowPair("2", "b/2", 2),
		// 删除
		s.MakeShadowPair("3", "c/3", 3),
		// 节点下线
		s.MakeShadowPair("4", "e/4", 4),
	}, nil, nil).AnyTimes()

	pairs := consul.KVPairs{
		{Key: "1", ModifyIndex: 1, CreateIndex: 1},
		{Key: "2", ModifyIndex: 20, CreateIndex: 2},
		{Key: "4", ModifyIndex: 4, CreateIndex: 3},
		{Key: "5", ModifyIndex: 5, CreateIndex: 4},
	}

	services := []*define.ServiceInfo{
		{ID: "a", Port: 2},
		{ID: "b", Port: 3},
		{ID: "c", Port: 4},
		// 新增
		{ID: "d", Port: 1},
	}

	s.NoError(s.dispatcher.Recover())
	// a/1 b/2 c/3 e/4

	s.kvAPI.EXPECT().DeleteTree("c/3", gomock.Any()).Return(nil, nil)
	s.kvAPI.EXPECT().DeleteTree("e/4", gomock.Any()).Return(nil, nil)

	updated := map[string]int{}
	s.kvAPI.EXPECT().Put(gomock.Any(), gomock.Any()).DoAndReturn(func(p *consul.KVPair, q *consul.WriteOptions) (*consul.WriteMeta, error) {
		pair, err := consul.GetSourceByShadowedPair(p)
		s.NoError(err)
		updated[p.Key] = int(pair.ModifyIndex)
		return nil, nil
	}).Times(3)

	s.NoError(s.dispatcher.Dispatch(pairs, services))
	// a/1 b/2 c/4 d/5

	s.Equal(20, updated["b/2"])
	s.Equal(4, updated["c/4"])
	s.Equal(5, updated["d/5"])
}

// TestDispatcherSuite :
func TestDispatcherSuite(t *testing.T) {
	suite.Run(t, new(DispatcherSuite))
}

// TriggerSuite :
type TriggerSuite struct {
	testsuite.TaskSuite

	config *consul.WatcherConfig
	client *MockClientAPI

	getConfigFromWatcher func(watcher define.ServiceWatcher) (config *consul.WatcherConfig, e error)
}

// SetupTest :
func (s *TriggerSuite) SetupTest() {
	s.TaskSuite.SetupTest()

	s.client = NewMockClientAPI(s.Ctrl)
	s.config = &consul.WatcherConfig{
		Context: s.CTX,
		Client:  s.client,
	}

	s.getConfigFromWatcher = consul.GetConfigFromWatcher
	consul.GetConfigFromWatcher = func(watcher define.ServiceWatcher) (*consul.WatcherConfig, error) {
		return s.config, nil
	}
}

// TearDownTest :
func (s *TriggerSuite) TearDownTest() {
	consul.GetConfigFromWatcher = s.getConfigFromWatcher
}

// ServiceTriggerSuite :
type ServiceTriggerSuite struct {
	TriggerSuite
	watcher *testsuite.MockServiceWatcher
	api     *MockKvAPI

	getConfigFromWatcher func(watcher define.ServiceWatcher) (config *consul.WatcherConfig, e error)
}

// SetupTest :
func (s *ServiceTriggerSuite) SetupTest() {
	s.TriggerSuite.SetupTest()

	s.watcher = testsuite.NewMockServiceWatcher(s.Ctrl)
	s.api = NewMockKvAPI(s.Ctrl)
	s.client.EXPECT().KV().Return(s.api).AnyTimes()
}

// TestUsage
func (s *ServiceTriggerSuite) TestServiceTrigger() {
	prefix := "ylp"

	ch := make(chan *consul.DispatchItem)
	defer close(ch)

	trigger := consul.NewServiceTriggerWithWatcher(s.watcher, prefix, ch)

	evCh := make(chan *define.WatchEvent)

	s.watcher.EXPECT().Start().Return(nil)
	s.watcher.EXPECT().Stop().Return(nil)
	s.watcher.EXPECT().Wait().Return(nil)
	s.watcher.EXPECT().Events().Return(evCh).AnyTimes()

	s.api.EXPECT().List(prefix, gomock.Any()).Return(nil, nil, nil)

	s.WithTaskRun(trigger, func() {
		evCh <- &define.WatchEvent{
			Data: make([]*define.ServiceInfo, 0),
		}
		item := <-ch
		s.NotNil(item)
	})
}

// TestServiceTrigger :
func TestServiceTrigger(t *testing.T) {
	suite.Run(t, new(ServiceTriggerSuite))
}

// PairTriggerSuite :
type PairTriggerSuite struct {
	TriggerSuite

	watcher *testsuite.MockServiceWatcher
	service *testsuite.MockService
}

// SetupTest :
func (s *PairTriggerSuite) SetupTest() {
	s.TriggerSuite.SetupTest()
	s.watcher = testsuite.NewMockServiceWatcher(s.Ctrl)
	s.service = testsuite.NewMockService(s.Ctrl)
}

// TestPairTrigger :
func (s *PairTriggerSuite) TestPairTrigger() {
	s.service.EXPECT().Info(define.ServiceTypeAll).Return(nil, nil)

	evCh := make(chan *define.WatchEvent)

	s.watcher.EXPECT().Start().Return(nil)
	s.watcher.EXPECT().Stop().Return(nil)
	s.watcher.EXPECT().Wait().Return(nil)
	s.watcher.EXPECT().Events().Return(evCh).AnyTimes()

	ch := make(chan *consul.DispatchItem)
	trigger := consul.NewPairTriggerWithWatcher(s.watcher, s.service, ch)
	s.WithTaskRun(trigger, func() {
		evCh <- &define.WatchEvent{
			Data: make(consul.KVPairs, 0),
		}
		item := <-ch
		s.NotNil(item)
	})
}

// TestPairTrigger :
func TestPairTrigger(t *testing.T) {
	suite.Run(t, new(PairTriggerSuite))
}

func TestReplaceFlowPrefix(t *testing.T) {
	cases := []struct {
		In, Out string
	}{
		{
			In:  `bk_bkmonitorv3_enterprise_production/service/v1/default/data_id/bkmonitorv3-2604497288/1001`,
			Out: `bk_bkmonitorv3_enterprise_production/service/v1/default/flow/bkmonitorv3-2604497288/1001`,
		},
		{
			In:  `flow/bkmonitorv3-2604497288/1001`,
			Out: ``,
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.Out, consul.ReplaceFlowPrefix(c.In))
	}
}

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
	"sync"
	"testing"

	"github.com/cstockton/go-conv"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// NotifyEvent :
type NotifyEvent struct {
	Index uint64
	Data  interface{}
}

// UpdatePairs :
func (s *PrefixDiffWatcherSuite) UpdatePairs(pairs map[string]*consul.KVPair, key string, modifyIndex uint64) consul.KVPairs {
	pairs[key] = &consul.KVPair{
		Key:         key,
		ModifyIndex: modifyIndex,
	}

	result := make(consul.KVPairs, 0, len(pairs))
	for _, value := range pairs {
		result = append(result, value)
	}

	return result
}

// WatcherSuite :
type WatcherSuite struct {
	ContextSuite
	newPlanByConfig func(conf *consul.WatcherConfig) (consul.WatchPlan, error)
	plan            *MockWatchPlan
	handler         consul.WatchHandlerFunc
	notifyChan      chan NotifyEvent
	config          *consul.WatcherConfig
}

// SetupTest :
func (s *WatcherSuite) SetupTest() {
	s.ContextSuite.SetupTest()
	s.notifyChan = make(chan NotifyEvent)
	s.plan = NewMockWatchPlan(s.Ctrl)
	s.config = &consul.WatcherConfig{}
	s.config = s.config.Init()

	s.plan.EXPECT().SetHandler(gomock.Any()).DoAndReturn(func(handler consul.WatchHandlerFunc) error {
		s.handler = handler
		return nil
	}).AnyTimes()
	consul.NewPlanByConfig = func(conf *consul.WatcherConfig) (plan consul.WatchPlan, e error) {
		return s.plan, nil
	}
}

// TearDownTest :
func (s *WatcherSuite) TearDownTest() {
	s.ContextSuite.TearDownTest()
	consul.NewPlanByConfig = s.newPlanByConfig
}

// WithWatcherRun :
func (s *WatcherSuite) WithWatcherRun(watcher define.ServiceWatcher, fn func()) {
	s.plan.EXPECT().Run(gomock.Any()).DoAndReturn(func(client consul.ClientAPI) error {
		for ev := range s.notifyChan {
			s.handler(ev.Index, ev.Data)
		}
		return nil
	})
	s.NoError(watcher.Start())
	fn()
	s.plan.EXPECT().IsStopped().Return(false)
	s.plan.EXPECT().Stop().DoAndReturn(func() error {
		close(s.notifyChan)
		return nil
	})
	s.NoError(watcher.Stop())
	s.NoError(watcher.Wait())
}

// CheckEventLeak :
func (s *WatcherSuite) CheckEventLeak(ch <-chan *define.WatchEvent) {
	for ev := range ch {
		s.Failf("event chan leak", "%v", ev)
	}
}

// NotifySync :
func (s *WatcherSuite) NotifySync(index uint64, data interface{}) {
	s.notifyChan <- NotifyEvent{
		Index: index,
		Data:  data,
	}
}

// Notify :
func (s *WatcherSuite) Notify(index uint64, data interface{}) {
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		s.NotifySync(index, data)
	}()

	wg.Wait()
}

// TestNotify :
func (s *WatcherSuite) TestNotify() {
	var expected uint64 = 1
	var result uint64
	watcher, err := consul.NewPlanWatcher(&consul.WatcherConfig{
		Handler: func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
			result = index
		},
	})

	s.NoError(err)
	s.WithWatcherRun(watcher, func() {
		s.NotifySync(expected, nil)
	})

	s.CheckEventLeak(watcher.Events())
	s.Equal(expected, result)
}

// TestWatcherSuite :
func TestWatcherSuite(t *testing.T) {
	suite.Run(t, new(WatcherSuite))
}

// KeyDiffWatcherSuite :
type KeyDiffWatcherSuite struct {
	WatcherSuite
}

// TestUsage :
func (s *KeyDiffWatcherSuite) TestUsage() {
	watcher, err := consul.NewKeyDiffWatcher(s.config, "test", false)
	s.NoError(err)

	count := 0
	cases := []struct {
		eventType define.WatchEventType
		pair      interface{}
	}{
		{define.WatchEventAdded, &consul.KVPair{
			CreateIndex: 0,
			ModifyIndex: 0,
		}},
		{define.WatchEventModified, &consul.KVPair{
			CreateIndex: 0,
			ModifyIndex: 1,
		}},
		{define.WatchEventDeleted, nil},
	}

	s.WithWatcherRun(watcher, func() {
		for _, c := range cases {
			count++
			s.Notify(0, c.pair)
			ev := <-watcher.Events()
			s.Equal(c.eventType, ev.Type)
		}
	})

	s.Equal(count, len(cases))
	s.CheckEventLeak(watcher.Events())
}

// TestKeyDiffWatcherSuite :
func TestKeyDiffWatcherSuite(t *testing.T) {
	suite.Run(t, new(KeyDiffWatcherSuite))
}

// PrefixDiffWatcherSuite :
type PrefixDiffWatcherSuite struct {
	WatcherSuite
}

// TestUsage :
func (s *PrefixDiffWatcherSuite) TestUsage() {
	watcher, err := consul.NewPrefixDiffWatcher(s.config, "test", false, true)
	s.NoError(err)
	pairs := make(map[string]*consul.KVPair)

	s.WithWatcherRun(watcher, func() {
		s.Notify(0, s.UpdatePairs(pairs, "test", 0))
		ev := <-watcher.Events()
		s.Equal(define.WatchEventAdded, ev.Type)

		s.Notify(0, s.UpdatePairs(pairs, "test", 1))
		ev = <-watcher.Events()
		s.Equal(define.WatchEventModified, ev.Type)

		s.Notify(0, s.UpdatePairs(pairs, "test", 2))
		ev = <-watcher.Events()
		s.Equal(define.WatchEventModified, ev.Type)

		s.Notify(0, make(consul.KVPairs, 0))
		ev = <-watcher.Events()
		s.Equal(define.WatchEventDeleted, ev.Type)
	})
	s.CheckEventLeak(watcher.Events())
}

// TestNoChange :
func (s *PrefixDiffWatcherSuite) TestNoChange() {
	watcher, err := consul.NewPrefixDiffWatcher(s.config, "test", false, true)
	s.NoError(err)
	pairs := make(map[string]*consul.KVPair)

	s.WithWatcherRun(watcher, func() {
		s.Notify(0, s.UpdatePairs(pairs, "test", 0))
		ev := <-watcher.Events()
		s.Equal(define.WatchEventAdded, ev.Type)

		s.Notify(0, s.UpdatePairs(pairs, "test", 0))
	})
	s.CheckEventLeak(watcher.Events())
}

// TestStockPairModify :
func (s *PrefixDiffWatcherSuite) TestStockPairModify() {
	watcher, err := consul.NewPrefixDiffWatcher(s.config, "test", false, true)
	s.NoError(err)
	pairs := make(map[string]*consul.KVPair)

	s.WithWatcherRun(watcher, func() {
		s.Notify(0, s.UpdatePairs(pairs, "test", 1))
		ev := <-watcher.Events()
		s.Equal(define.WatchEventAdded, ev.Type)
	})
	s.CheckEventLeak(watcher.Events())
}

// TestStockPairMixed :
func (s *PrefixDiffWatcherSuite) TestStockPairMixed() {
	watcher, err := consul.NewPrefixDiffWatcher(s.config, "test", false, true)
	s.NoError(err)
	pairs := make(map[string]*consul.KVPair)

	s.WithWatcherRun(watcher, func() {
		s.UpdatePairs(pairs, "1", 1)
		s.Notify(0, s.UpdatePairs(pairs, "2", 0))

		score := 0
		ev := <-watcher.Events()
		s.Equal(define.WatchEventAdded, ev.Type)
		score += conv.Int(ev.ID)

		ev = <-watcher.Events()
		s.Equal(define.WatchEventAdded, ev.Type)
		score += conv.Int(ev.ID)

		s.Equal(3, score)
	})
	s.CheckEventLeak(watcher.Events())
}

// TestStockModifyMixed :
func (s *PrefixDiffWatcherSuite) TestStockModifyMixed() {
	watcher, err := consul.NewPrefixDiffWatcher(s.config, "test", false, true)
	s.NoError(err)
	pairs := make(map[string]*consul.KVPair)

	s.WithWatcherRun(watcher, func() {
		s.UpdatePairs(pairs, "1", 0)
		s.Notify(0, s.UpdatePairs(pairs, "2", 0))
		score := 0
		ev := <-watcher.Events()
		s.Equal(define.WatchEventAdded, ev.Type)
		score += conv.Int(ev.ID)
		ev = <-watcher.Events()
		s.Equal(define.WatchEventAdded, ev.Type)
		score += conv.Int(ev.ID)
		s.Equal(3, score)

		s.Notify(0, s.UpdatePairs(pairs, "1", 1))
		ev = <-watcher.Events()
		s.Equal(define.WatchEventModified, ev.Type)
		s.Equal("1", ev.ID)

		s.Notify(0, make(consul.KVPairs, 0))
		score = 0
		ev = <-watcher.Events()
		s.Equal(define.WatchEventDeleted, ev.Type)
		score += conv.Int(ev.ID)
		ev = <-watcher.Events()
		s.Equal(define.WatchEventDeleted, ev.Type)
		score += conv.Int(ev.ID)
		s.Equal(3, score)
	})
	s.CheckEventLeak(watcher.Events())
}

// TestPrefixDiffWatcherSuite :
func TestPrefixDiffWatcherSuite(t *testing.T) {
	suite.Run(t, new(PrefixDiffWatcherSuite))
}

// KeySnapshotWatcherSuite :
type KeySnapshotWatcherSuite struct {
	WatcherSuite
}

// TestUsage :
func (s *KeySnapshotWatcherSuite) TestUsage() {
	watcher, err := consul.NewKeySnapshotWatcher(s.config, "test", false)
	s.NoError(err)

	count := 0
	cases := []*consul.KVPair{new(consul.KVPair), new(consul.KVPair), nil}

	s.WithWatcherRun(watcher, func() {
		for _, c := range cases {
			count++
			s.Notify(0, c)
			ev := <-watcher.Events()
			s.Equal(define.WatchEventModified, ev.Type)
			s.Equal(c, ev.Data)
		}
	})

	s.Equal(count, len(cases))
	s.CheckEventLeak(watcher.Events())
}

// TestKeySnapshotWatcher :
func TestKeySnapshotWatcher(t *testing.T) {
	suite.Run(t, new(KeySnapshotWatcherSuite))
}

// PrefixBatchDiffWatcherSuite :
type PrefixBatchDiffWatcherSuite struct {
	WatcherSuite
}

// TestUsage :
func (s *PrefixBatchDiffWatcherSuite) TestUsage() {
	watcher, err := consul.NewPrefixBatchDiffWatcher(s.config, "test", false, true)
	s.NoError(err)

	cases := []struct {
		pairs                    consul.KVPairs
		added, modified, deleted int
	}{
		{consul.KVPairs{
			&consul.KVPair{Key: "1"},
			&consul.KVPair{Key: "2"},
		}, 2, 0, 0},
		{consul.KVPairs{
			&consul.KVPair{Key: "2"},
		}, 0, 0, 1},
		{consul.KVPairs{
			&consul.KVPair{Key: "2"},
			&consul.KVPair{Key: "3"},
		}, 1, 0, 0},
		{consul.KVPairs{
			&consul.KVPair{Key: "2", ModifyIndex: 1},
			&consul.KVPair{Key: "3"},
		}, 0, 1, 0},
		{nil, 0, 0, 2},
	}

	s.WithWatcherRun(watcher, func() {
		for i, c := range cases {
			s.Notify(0, c.pairs)
			events := <-watcher.Events()
			s.Equal(define.WatchEventModified, events.Type, i)

			added := 0
			modified := 0
			deleted := 0

			for _, ev := range events.Data.([]*define.WatchEvent) {
				switch ev.Type {
				case define.WatchEventAdded:
					added++
				case define.WatchEventModified:
					modified++
				case define.WatchEventDeleted:
					deleted++
				}
			}
			s.Equal(c.added, added, i)
			s.Equal(c.modified, modified, i)
			s.Equal(c.deleted, deleted, i)
		}
	})
}

// TestPrefixBatchDiffWatcher :
func TestPrefixBatchDiffWatcher(t *testing.T) {
	suite.Run(t, new(PrefixBatchDiffWatcherSuite))
}

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
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// BatchShadowCopierSuite
type BatchShadowCopierSuite struct {
	testsuite.TaskSuite
	conf                 consul.BatchShadowCopierConfig
	client               *MockClientAPI
	api                  *MockKvAPI
	getClientFromSession func(source define.Session) (consul.ClientAPI, error)
}

// SetupTest
func (s *BatchShadowCopierSuite) SetupTest() {
	s.TaskSuite.SetupTest()
	s.client = NewMockClientAPI(s.Ctrl)
	s.api = NewMockKvAPI(s.Ctrl)
	s.conf = consul.BatchShadowCopierConfig{
		Context:  s.CTX,
		Prefix:   "source",
		Interval: time.Millisecond,
		Client:   s.client,
	}

	s.client.EXPECT().KV().Return(s.api).AnyTimes()

	s.getClientFromSession = consul.GetClientFromSession
	consul.GetClientFromSession = func(source define.Session) (api consul.ClientAPI, e error) {
		return s.client, nil
	}
}

// TearDownTest
func (s *BatchShadowCopierSuite) TearDownTest() {
	consul.GetClientFromSession = s.getClientFromSession
}

// Store
func (s *BatchShadowCopierSuite) Store(key string, pair *consul.KVPair, err error) *gomock.Call {
	return s.api.EXPECT().Get(key, gomock.Any()).Return(pair, nil, err)
}

// ShadowWatch
func (s *BatchShadowCopierSuite) ShadowWatch(ch chan *consul.KVPair) {
	s.api.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil, nil).Do(func(p *consul.KVPair, q *consul.WriteOptions) (*consul.WriteMeta, error) {
		var pair consul.KVPair
		s.NoError(json.Unmarshal(p.Value, &pair))
		ch <- &pair
		return nil, nil
	}).AnyTimes()
}

// TestLink
func (s *BatchShadowCopierSuite) TestLink() {
	conf := s.conf
	copier := consul.NewBatchShadowCopierWithWatcher(conf)

	cases := []struct {
		source, target       string
		rawSource, rawTarget string
		result               bool
		count                int
	}{
		{"a", "b", "a", "b", true, 1},
		{"a", "b", "a", "b", false, 1},
		{"a", "c", "a", "c", true, 2},
	}

	for i, c := range cases {
		s.Equal(c.result, copier.Link(c.source, c.target, ""), i)
		found := false
		count := 0
		copier.Each(func(source, target string, info *consul.ShadowInfo) bool {
			count++
			if c.rawSource == source && c.rawTarget == target {
				found = true
			}
			return true
		})
		s.True(found, i)
		s.Equal(c.count, count, i)
	}
}

// TestUnlink
func (s *BatchShadowCopierSuite) TestUnlink() {
	conf := s.conf
	copier := consul.NewBatchShadowCopierWithWatcher(conf)

	s.True(copier.Link("a", "b", ""))

	cases := []struct {
		source, target       string
		rawSource, rawTarget string
		result               bool
		count                int
	}{
		{"a", "c", "a", "c", false, 1},
		{"a", "b", "a", "b", true, 0},
		{"a", "b", "a", "b", false, 0},
	}

	for i, c := range cases {
		s.Equal(c.result, copier.Unlink(c.source, c.target), i)
		found := false
		count := 0
		copier.Each(func(source, target string, info *consul.ShadowInfo) bool {
			count++
			if c.rawSource == source && c.rawTarget == target {
				found = true
			}
			return true
		})
		s.False(found, i)
		s.Equal(c.count, count, i)
	}
}

// TestSync
func (s *BatchShadowCopierSuite) TestSync() {
	conf := s.conf
	copier := consul.NewBatchShadowCopierWithWatcher(conf)
	shadowSync := consul.ShadowSync
	defer func() {
		consul.ShadowSync = shadowSync
	}()

	source := "a"
	s.True(copier.Link(source, "b", ""))

	cases := []struct {
		source, target             string
		shadowSource, shadowTarget string
		err                        error
	}{
		{"a", "b", "a", "b", nil},
		{"x", "b", "", "b", nil},
		{"a", "c", "", "c", nil},
		{"a", "b", "a", "b", fmt.Errorf("test")},
	}

	ch := make(chan *consul.KVPair, 1)
	defer close(ch)

	for i, c := range cases {
		var wg sync.WaitGroup
		wg.Add(1)
		consul.ShadowSync = func(ctx context.Context, client consul.ClientAPI, source string, target ...string) (err error) {
			s.Equal(c.shadowSource, source, i)
			s.Equal(c.shadowTarget, target[0], i)
			wg.Done()
			return c.err
		}

		err := copier.Sync(c.source, c.target)
		s.Equal(c.err, err, i)
		wg.Wait()
	}
}

// TestSyncAll
func (s *BatchShadowCopierSuite) TestSyncAll() {
	conf := s.conf
	copier := consul.NewBatchShadowCopierWithWatcher(conf)
	shadowSync := consul.ShadowSync
	defer func() {
		consul.ShadowSync = shadowSync
	}()

	sourceA := consul.KVPair{
		Key:         "a1",
		ModifyIndex: 1,
	}
	sourceB := consul.KVPair{
		Key:         "b1",
		ModifyIndex: 2,
	}

	s.True(copier.Link("a1", "a2", ""))
	s.True(copier.Link("b1", "b2", ""))
	s.True(copier.Link("a1", "c2", ""))

	ch := make(chan struct {
		source, target string
	}, 3)
	consul.ShadowSync = func(ctx context.Context, client consul.ClientAPI, source string, targets ...string) (err error) {
		for _, target := range targets {
			ch <- struct{ source, target string }{source: source, target: target}
		}
		return nil
	}

	s.NoError(copier.SyncAll())
	close(ch)

	for i := range ch {
		switch i.target {
		case "a2":
			s.Equal(sourceA.Key, i.source)
		case "b2":
			s.Equal(sourceB.Key, i.source)
		case "c2":
			s.Equal(sourceA.Key, i.source)
		}
	}
}

// TestShadow
func (s *BatchShadowCopierSuite) TestShadow() {
	conf := s.conf

	copier := consul.NewBatchShadowCopierWithWatcher(conf)

	sourceA := &consul.KVPair{
		Key:         "a1",
		Value:       []byte("a"),
		ModifyIndex: 1,
	}
	sourceB := &consul.KVPair{
		Key:         "b1",
		Value:       []byte("b"),
		ModifyIndex: 2,
	}
	sourceC := &consul.KVPair{
		Key:         "c1",
		Value:       []byte("c"),
		ModifyIndex: 0,
	}

	s.True(copier.Link("a1", "a2", ""))
	s.True(copier.Link("b1", "b2", ""))

	ch := make(chan *consul.KVPair, 1)
	defer close(ch)
	s.ShadowWatch(ch)

	shadowSync := consul.ShadowSync
	defer func() {
		consul.ShadowSync = shadowSync
	}()

	var countA, countB, countC int
	var onceA, onceB, onceC sync.Once
	var wg sync.WaitGroup

	wg.Add(2)

	consul.ShadowSync = func(ctx context.Context, client consul.ClientAPI, source string, target ...string) (err error) {
		switch source {
		case sourceA.Key:
			onceA.Do(func() {
				countA++
				wg.Done()
			})
		case sourceB.Key:
			onceB.Do(func() {
				countB++
				wg.Done()
			})
		case sourceC.Key:
			onceC.Do(func() {
				countC++
				wg.Done()
			})
		default:
			s.Fail("unsupported type")
		}
		return nil
	}

	s.WithTaskRun(copier, func() {
		wg.Wait()
	})

	s.Equal(1, countA)
	s.Equal(1, countB)
	s.Equal(0, countC)
}

// TestBatchShadowCopier
func TestBatchShadowCopier(t *testing.T) {
	suite.Run(t, new(BatchShadowCopierSuite))
}

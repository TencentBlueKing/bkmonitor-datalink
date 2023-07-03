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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/suite"

	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// SessionSuite
type SessionSuite struct {
	ConsulSuite
	config SessionConfig
}

// SetupTest
func (s *SessionSuite) SetupTest() {
	s.ConsulSuite.SetupTest()
	s.config = SessionConfig{
		ID:        "id",
		Name:      "Name",
		Node:      "node",
		LockDelay: time.Millisecond,
		Behavior:  "",
		TTL:       "1ms",
		Namespace: "namespace",
	}
}

// Store
func (s *SessionSuite) Store(kv *MockKvAPI, key, value string, expires time.Duration, apiErr error) *KVPair {
	flags := KVPairTransaction
	if expires != 0 && apiErr == nil {
		flags |= KVPairExpires
		expiresKey := fmt.Sprintf("%s/%s", key, KVPairAttrKeyExpires)
		expiresAt := time.Now().Add(expires)
		kv.EXPECT().Get(expiresKey, gomock.Any()).Return(&KVPair{
			Key:   expiresKey,
			Value: []byte(expiresAt.Format(time.RFC3339)),
			Flags: KVPairTransaction | KVPairMetaAttr,
		}, &QueryMeta{}, nil)
	}
	pair := KVPair{
		Key:   key,
		Value: []byte(value),
		Flags: flags,
	}
	kv.EXPECT().Get(key, gomock.Any()).Return(&pair, &QueryMeta{}, apiErr)
	return &pair
}

// TestGet
func (s *SessionSuite) TestGet() {
	cases := []struct {
		key, value string
		err        error
		expire     time.Duration
		path       string
		result     []byte
		apiError   error
		wait       time.Duration
	}{
		{
			"a", "ylp", nil, time.Hour,
			"namespace/a", []byte("ylp"),
			nil, 0,
		},
		{
			"b", "ylp", define.ErrItemNotFound, time.Nanosecond,
			"namespace/b", nil,
			nil, time.Millisecond,
		},
		{
			"/c", "ylp", nil, time.Hour,
			"c", []byte("ylp"),
			nil, 0,
		},
		{
			"/d", "ylp", define.ErrItemNotFound, time.Nanosecond,
			"d", nil,
			nil, time.Millisecond,
		},
		{
			"e", "ylp", s.apiError, time.Hour,
			"namespace/e", nil,
			s.apiError, 0,
		},
	}

	for i, c := range cases {
		session := NewTransactionalSession(s.CTX, s.client, s.config)

		s.Store(s.kv, c.path, c.value, c.expire, c.apiError)
		if c.wait != 0 {
			<-time.After(c.wait)
		}
		data, err := session.Get(c.key)
		s.Equal(c.err, errors.Cause(err), i)
		s.Equal(c.result, data, i)
	}
}

// TestMissing
func (s *SessionSuite) TestMissing() {
	key := "test"
	s.kv.EXPECT().Get("namespace/"+key, gomock.Any()).Return(nil, &QueryMeta{}, nil)

	session := NewTransactionalSession(s.CTX, s.client, s.config)
	data, err := session.Get(key)
	s.Equal(define.ErrItemNotFound, errors.Cause(err))
	s.Equal([]byte(nil), data)
}

// TestExists
func (s *SessionSuite) TestExists() {
	cases := []struct {
		key, path, value string
		result           bool
		err              error
		expire, wait     time.Duration
	}{
		{
			"test", "namespace/test", "ylp",
			true, nil, time.Hour, 0,
		},
		{
			"test", "namespace/test", "ylp",
			false, nil, time.Nanosecond, time.Millisecond,
		},
		{
			"/test", "test", "ylp",
			true, nil, time.Hour, 0,
		},
		{
			"/test", "test", "ylp",
			false, nil, time.Nanosecond, time.Millisecond,
		},
	}

	for _, c := range cases {
		session := NewTransactionalSession(s.CTX, s.client, s.config)

		s.Store(s.kv, c.path, c.value, c.expire, nil)
		if c.wait != 0 {
			<-time.After(c.wait)
		}
		result, err := session.Exists(c.key)
		s.Equal(c.err, errors.Cause(err))
		s.Equal(c.result, result)
	}
}

// TestSetNoExpires
func (s *SessionSuite) TestSetNoExpires() {
	cases := []struct {
		key, value string
		err        error
		path       string
		ok         bool
		apiError   error
	}{
		{
			"test", "ylp", nil,
			"namespace/test", true, nil,
		},
		{
			"test", "ylp", define.ErrItemAlreadyExists,
			"namespace/test", false, nil,
		},
		{
			"/test", "ylp", nil,
			"test", true, nil,
		},
		{
			"/test", "ylp", define.ErrItemAlreadyExists,
			"test", false, nil,
		},
		{
			"test", "ylp", s.apiError,
			"namespace/test", false, s.apiError,
		},
	}

	for i, c := range cases {
		session := NewTransactionalSession(s.CTX, s.client, s.config)
		s.kv.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(func(p *KVPair, q *WriteOptions) (bool, *WriteMeta, error) {
			s.Equal(c.path, p.Key, i)
			s.Equal([]byte(c.value), p.Value, i)
			s.Equal(s.config.ID, p.Session, i)
			s.Equal(uint64(0), p.Flags&KVPairExpires)
			return c.ok, &WriteMeta{}, c.apiError
		})
		err := session.Set(c.key, []byte(c.value), define.StoreNoExpires)
		s.Equal(c.err, errors.Cause(err), i)
	}
}

// TestScan
func (s *SessionSuite) TestScan() {
	cases := map[string]struct {
		key string
		ok  bool
	}{
		"namespace/abc": {"abc", true},
		"namespace/bcd": {"bcd", false},
		"namespace/ab":  {"ab", true},
	}
	prefix := "ab"
	count := 0
	keys := make([]string, 0)
	for path, c := range cases {
		if strings.HasPrefix(c.key, prefix) {
			s.Store(s.kv, path, "ylp", define.StoreNoExpires, nil)
			keys = append(keys, path)
			count++
		}
	}

	s.kv.EXPECT().Keys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, &QueryMeta{}, nil)

	session := NewTransactionalSession(s.CTX, s.client, s.config)
	visited := 0
	s.NoError(session.Scan(prefix, func(path string, data []byte) bool {
		c, ok := cases[path]
		s.True(ok)
		s.Equal(fmt.Sprintf("namespace/%s", c.key), path, c.key)
		s.True(c.ok, path)
		visited++
		return true
	}))
	s.Equal(count, visited)
}

// TestDelete
func (s *SessionSuite) TestDelete() {
	key := "test"
	s.kv.EXPECT().DeleteTree("namespace/"+key, gomock.Any()).Return(&WriteMeta{}, nil)

	session := NewTransactionalSession(s.CTX, s.client, s.config)
	s.NoError(session.Delete(key))
}

// TestOpen
func (s *SessionSuite) TestOpen() {
	id := "test"
	s.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(se *SessionEntry, q *WriteOptions) (string, *WriteMeta, error) {
		s.Equal(s.config.Name, se.Name)
		s.Equal(s.config.Node, se.Node)
		s.Equal(s.config.LockDelay, se.LockDelay)
		s.Equal(s.config.Behavior, se.Behavior)
		s.Equal(s.config.TTL, se.TTL)
		return id, &WriteMeta{}, nil
	})

	session := NewTransactionalSession(s.CTX, s.client, s.config)
	s.NoError(session.Open())
	s.Equal(id, session.ID)
}

// TestClose
func (s *SessionSuite) TestClose() {
	var wg sync.WaitGroup
	wg.Add(1)
	s.session.EXPECT().Destroy(s.config.ID, gomock.Any()).DoAndReturn(func(id string, q *WriteOptions) (*WriteMeta, error) {
		ctx := q.Context()
		go func() {
			<-ctx.Done()
			wg.Done()
		}()
		return &WriteMeta{}, nil
	})

	session := NewTransactionalSession(s.CTX, s.client, s.config)
	s.NoError(session.Close())
	wg.Wait()
}

// TestCommit
func (s *SessionSuite) TestCommit() {
	id := s.config.ID
	s.session.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(func(id string, q *WriteOptions) (*SessionEntry, *WriteMeta, error) {
		s.Equal(s.config.ID, id)
		return &SessionEntry{
			ID: id + id,
		}, &WriteMeta{}, nil
	})

	session := NewTransactionalSession(s.CTX, s.client, s.config)
	s.NoError(session.Commit())
	s.Equal(id+id, session.ID)
}

// TestReopen
func (s *SessionSuite) TestReopen() {
	id := "test"
	s.session.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(func(id string, q *WriteOptions) (*SessionEntry, *WriteMeta, error) {
		s.Equal(s.config.ID, id)
		return nil, &WriteMeta{}, nil
	})

	s.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(se *SessionEntry, q *WriteOptions) (string, *WriteMeta, error) {
		s.Equal(s.config.Name, se.Name)
		s.Equal(s.config.Node, se.Node)
		s.Equal(s.config.LockDelay, se.LockDelay)
		s.Equal(s.config.Behavior, se.Behavior)
		s.Equal(s.config.TTL, se.TTL)
		return id, &WriteMeta{}, nil
	})

	session := NewTransactionalSession(s.CTX, s.client, s.config)
	s.NoError(session.Commit())
	s.Equal(id, session.ID)
}

// TestSessionSuite
func TestSessionSuite(t *testing.T) {
	suite.Run(t, new(SessionSuite))
}

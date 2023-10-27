// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"fmt"
	"strings"
	"time"

	consul "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	SessionBehaviorRelease = consul.SessionBehaviorRelease
	SessionBehaviorDelete  = consul.SessionBehaviorDelete
)

const (
	KVPairNotSet      uint64 = 0
	KVPairMetaAttr    uint64 = 1
	KVPairExpires     uint64 = 1 << 2
	KVPairTransaction uint64 = 1 << 3
)

const (
	KVPairAttrKeyExpires = "expires"
)

// SessionConfig : represents a serviceSession in consul
type SessionConfig struct {
	ID        string
	Name      string
	Node      string
	LockDelay time.Duration
	Behavior  string
	TTL       string
	Namespace string
	Checks    []string
	Flags     uint64
}

// QueryOptions
type QueryOptions = consul.QueryOptions

// Session
type Session struct {
	SessionConfig
	Client ClientAPI

	ctx      context.Context
	cancelFn context.CancelFunc
}

// Clone
func (s Session) Clone() *Session {
	return &s
}

// String
func (s *Session) String() string {
	return fmt.Sprintf("%v[%v]", s.Name, s.ID)
}

// AbsPath
func (s *Session) AbsPath(key string) string {
	key = utils.ResolveUnixPath(s.Namespace, key)
	return strings.TrimPrefix(key, "/")
}

// AttrPath
func (s *Session) AttrPath(key, attr string) string {
	return fmt.Sprintf("%s/%s", s.AbsPath(key), attr)
}

func (s *Session) get(key string) (*KVPair, error) {
	logging.Debugf("consul session get key %s", key)

	pair, _, err := s.Client.KV().Get(key, NewQueryOptions(s.ctx))
	if err != nil {
		return nil, errors.Wrapf(err, "get key %v error", key)
	} else if pair == nil {
		return nil, define.ErrItemNotFound
	}

	return pair, nil
}

// Get
func (s *Session) Get(key string) ([]byte, error) {
	pair, err := s.get(s.AbsPath(key))
	if err != nil {
		return nil, err
	}

	item := define.NewStoreItem(pair.Value, define.StoreNoExpires)
	if pair.Flags&KVPairExpires != 0 {
		expiresPair, err := s.get(s.AttrPath(key, KVPairAttrKeyExpires))
		if err != nil {
			return nil, err
		}
		expiresAt, err := time.Parse(time.RFC3339, string(expiresPair.Value))
		if err != nil {
			return nil, errors.Wrapf(err, "parse expires time %v failed", expiresPair.Value)
		}
		item.SetExpiresAt(expiresAt)
	}

	if item.IsExpired() {
		return nil, define.ErrItemNotFound
	}

	return pair.Value, nil
}

// Exists
func (s *Session) Exists(key string) (bool, error) {
	data, err := s.Get(key)
	switch err {
	case define.ErrItemNotFound:
		return false, nil
	default:
		return data != nil, err
	}
}

func (s *Session) set(key string, data []byte, flags uint64) error {
	api := s.Client.KV()

	var ok bool
	var err error
	pair := &consul.KVPair{
		Key:     key,
		Value:   data,
		Session: s.ID,
		Flags:   flags,
	}
	opts := NewWriteOptions(s.ctx)

	flags |= s.Flags
	if flags&KVPairTransaction != 0 {
		logging.Debugf("consul session acquire %s", pair.Key)
		ok, _, err = api.Acquire(pair, opts)
	} else {
		logging.Debugf("consul session put %s", pair.Key)
		_, err = api.Put(pair, opts)
		ok = true
	}

	if err != nil {
		return errors.WithStack(err)
	}

	if !ok {
		return define.ErrItemAlreadyExists
	}

	return nil
}

// Set
func (s *Session) Set(key string, data []byte, expires time.Duration) error {
	item := define.NewStoreItem(data, expires)
	flags := KVPairNotSet
	if item.ExpiresAt != nil {
		flags |= KVPairExpires
		err := s.set(
			s.AttrPath(key, KVPairAttrKeyExpires),
			[]byte(item.ExpiresAt.Format(time.RFC3339)),
			KVPairMetaAttr,
		)
		if err != nil {
			return err
		}
	}
	return s.set(s.AbsPath(key), data, flags)
}

// Scan
func (s *Session) Scan(prefix string, callback define.StoreScanCallback, withAll ...bool) error {
	keys, _, err := s.Client.KV().Keys(s.AbsPath(prefix), "", NewQueryOptions(s.ctx))
	if err != nil {
		return errors.WithStack(err)
	}

	for _, key := range keys {
		data, err := s.get(key)
		if err != nil {
			return errors.WithStack(err)
		}
		if data.Flags&KVPairMetaAttr != 0 {
			continue
		}
		if !callback(key, data.Value) {
			break
		}
	}
	return nil
}

// Delete
func (s *Session) Delete(key string) error {
	key = s.AbsPath(key)

	logging.Debugf("consul session delete tree %s", key)
	_, err := s.Client.KV().DeleteTree(key, NewWriteOptions(s.ctx))
	return errors.WithStack(err)
}

// Open
func (s *Session) Open() error {
	id, _, err := s.Client.Session().Create(&SessionEntry{
		Name:      s.Name,
		Node:      s.Node,
		LockDelay: s.LockDelay,
		Behavior:  s.Behavior,
		TTL:       s.TTL,
	}, NewWriteOptions(s.ctx))
	if err != nil {
		return errors.WithStack(err)
	}
	s.ID = id
	return nil
}

// Close
func (s *Session) Close() error {
	_, err := s.Client.Session().Destroy(s.ID, NewWriteOptions(s.ctx))
	s.cancelFn()
	return errors.WithStack(err)
}

func (s *Session) CommitReOpen() (bool, error) {
	var reOpen bool
	entry, _, err := s.Client.Session().Renew(s.ID, NewWriteOptions(s.ctx))
	if err != nil {
		return reOpen, errors.WithStack(err)
	} else if entry != nil { // nil when checks failed
		s.ID = entry.ID
	} else {
		err = s.Open()
		reOpen = true
	}
	return reOpen, err
}

// Commit
func (s *Session) Commit() error {
	_, err := s.CommitReOpen()
	return err
}

// PutCache :
func (s *Session) PutCache(key string, data []byte, expires time.Duration) error {
	return nil
}

// Batch :
func (s *Session) Batch() error {
	return nil
}

// NewSession
func NewSession(ctx context.Context, client ClientAPI, config SessionConfig) *Session {
	ctx, cancel := context.WithCancel(ctx)
	return &Session{
		ctx:           ctx,
		cancelFn:      cancel,
		SessionConfig: config,
		Client:        client,
	}
}

// NewTransactionalSession
func NewTransactionalSession(ctx context.Context, client ClientAPI, config SessionConfig) *Session {
	config.Flags |= KVPairTransaction
	return NewSession(ctx, client, config)
}

// NewPersistentSession
func NewPersistentSession(ctx context.Context, client ClientAPI, config SessionConfig) *Session {
	config.Flags &= ^KVPairTransaction
	return NewSession(ctx, client, config)
}

// DetectSession
func DetectSession(session define.Session) (*Session, error) {
	switch s := session.(type) {
	case *Session:
		return s, nil
	default:
		return nil, errors.WithMessagef(define.ErrType, "%T not supported", s)
	}
}

// CloneSession
var CloneSession = func(ctx context.Context, source define.Session) (*Session, error) {
	session, err := DetectSession(source)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	cloned := session.Clone()
	cloned.ctx = ctx
	cloned.cancelFn = cancel
	return cloned, nil
}

// CloneTransactionalSession
var CloneTransactionalSession = func(ctx context.Context, source define.Session) (*Session, error) {
	session, err := CloneSession(ctx, source)
	if err != nil {
		return nil, err
	}
	session.SessionConfig.Flags |= KVPairTransaction
	return session, nil
}

// CloneTransactionalSession
var ClonePersistentSession = func(ctx context.Context, source define.Session) (*Session, error) {
	session, err := CloneSession(ctx, source)
	if err != nil {
		return nil, err
	}
	session.SessionConfig.Flags &= ^KVPairTransaction
	return session, nil
}

// GetConfigFromSession
var GetConfigFromSession = func(source define.Session) (*SessionConfig, error) {
	session, err := DetectSession(source)
	if err != nil {
		return nil, err
	}
	return &session.SessionConfig, nil
}

// GetClientFromSession
var GetClientFromSession = func(source define.Session) (ClientAPI, error) {
	session, err := DetectSession(source)
	if err != nil {
		return nil, err
	}
	return session.Client, nil
}

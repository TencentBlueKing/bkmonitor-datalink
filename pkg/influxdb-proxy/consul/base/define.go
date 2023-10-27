// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package base

import (
	"log"

	"github.com/hashicorp/consul/api"
)

// ConsulClient :
type ConsulClient interface {
	Put(path string, value []byte) error
	Get(path string) (*api.KVPair, error)
	Delete(path string) error
	GetPrefix(prefix string, separator string) (api.KVPairs, error)
	GetChild(prefix string, separator string) ([]string, error)
	Watch(path string, separator string) (<-chan interface{}, error)
	StopWatch(path string, planType string) error
	Close() error

	ServiceRegister(serviceName string) error
	ServiceDeregister(serviceID string) error
	ServiceAwake(serviceID string) error
	CheckRegister(serviceID string, checkID string, ttl string) error
	CheckDeregister(checkID string) error
	CheckFail(checkID, note string) error
	CheckPass(checkID, note string) error
	CheckStatus(checkID string) (string, error)
	CAS(path string, preValue []byte, value []byte) (bool, error)

	Acquire(path string, sessionID string) (bool, error)
	Release(path string, sessionID string) (bool, error)

	NewSessionID(ttl string) (string, error)
	RenewSession(sessionID string) error
}

// KV 查询接口
type KV interface {
	Get(key string, q *api.QueryOptions) (*api.KVPair, *api.QueryMeta, error)
	List(prefix string, q *api.QueryOptions) (api.KVPairs, *api.QueryMeta, error)
	Keys(prefix, separator string, q *api.QueryOptions) ([]string, *api.QueryMeta, error)
	Put(p *api.KVPair, q *api.WriteOptions) (*api.WriteMeta, error)
	CAS(p *api.KVPair, q *api.WriteOptions) (bool, *api.WriteMeta, error)
	Acquire(p *api.KVPair, q *api.WriteOptions) (bool, *api.WriteMeta, error)
	Release(p *api.KVPair, q *api.WriteOptions) (bool, *api.WriteMeta, error)
	Delete(key string, w *api.WriteOptions) (*api.WriteMeta, error)
	DeleteCAS(p *api.KVPair, q *api.WriteOptions) (bool, *api.WriteMeta, error)
	DeleteTree(prefix string, w *api.WriteOptions) (*api.WriteMeta, error)
	Txn(txn api.KVTxnOps, q *api.QueryOptions) (bool, *api.KVTxnResponse, *api.QueryMeta, error)
}

// Agent :
type Agent interface {
	AgentHealthServiceByID(serviceID string) (string, *api.AgentServiceChecksInfo, error)
	ServiceRegister(service *api.AgentServiceRegistration) error
	ServiceDeregister(serviceID string) error
	CheckRegister(check *api.AgentCheckRegistration) error
	CheckDeregister(checkID string) error
	ChecksWithFilter(filter string) (map[string]*api.AgentCheck, error)
	PassTTL(checkID, note string) error
	FailTTL(checkID, note string) error
}

// Plan 监听接口
type Plan interface {
	Run(address string) error
	RunWithConfig(address string, conf *api.Config) error
	RunWithClientAndLogger(client *api.Client, logger *log.Logger) error
	Stop()
	IsStopped() bool
}

type Session interface {
	Create(se *api.SessionEntry, q *api.WriteOptions) (string, *api.WriteMeta, error)
	Destroy(id string, q *api.WriteOptions) (*api.WriteMeta, error)
	Renew(id string, q *api.WriteOptions) (*api.SessionEntry, *api.WriteMeta, error)
}

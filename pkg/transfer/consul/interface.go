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
	"fmt"
	"log"

	consul "github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/api/watch"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// SessionEntry
type SessionEntry = consul.SessionEntry

// WriteOptions
type WriteOptions = consul.WriteOptions

// WriteMeta
type WriteMeta = consul.WriteMeta

// QueryMeta
type QueryMeta = consul.QueryMeta

// AgentServiceRegistration
type AgentServiceRegistration = consul.AgentServiceRegistration

// AgentService
type AgentService = consul.AgentService

// ServiceEntry
type ServiceEntry = consul.ServiceEntry

// KVPair
type KVPair = consul.KVPair

// KVPairs
type KVPairs = consul.KVPairs

// WatchHandlerFunc
type WatchHandlerFunc = watch.HandlerFunc

// ClientAPI
type ClientAPI interface {
	Raw() *consul.Client
	KV() KvAPI
	Session() SessionAPI
	Agent() AgentAPI
	Health() HealthAPI
}

// SessionAPI
type SessionAPI interface {
	Create(se *SessionEntry, q *WriteOptions) (string, *WriteMeta, error)
	Destroy(id string, q *WriteOptions) (*WriteMeta, error)
	Renew(id string, q *WriteOptions) (*SessionEntry, *WriteMeta, error)
}

// KvAPI
type KvAPI interface {
	Get(key string, q *QueryOptions) (*KVPair, *QueryMeta, error)
	Acquire(p *KVPair, q *WriteOptions) (bool, *WriteMeta, error)
	Release(p *KVPair, q *WriteOptions) (bool, *WriteMeta, error)
	Put(p *KVPair, q *WriteOptions) (*WriteMeta, error)
	Keys(prefix, separator string, q *QueryOptions) ([]string, *QueryMeta, error)
	List(prefix string, q *QueryOptions) (KVPairs, *QueryMeta, error)
	Delete(key string, w *WriteOptions) (*WriteMeta, error)
	DeleteTree(prefix string, w *WriteOptions) (*WriteMeta, error)
}

// AgentAPI
type AgentAPI interface {
	ServiceRegister(service *AgentServiceRegistration) error
	EnableServiceMaintenance(serviceID, reason string) error
	DisableServiceMaintenance(serviceID string) error
	Service(serviceID string, q *QueryOptions) (*AgentService, *QueryMeta, error)
	ServiceDeregister(serviceID string) error
	UpdateTTL(checkID, output, status string) error
	Services() (map[string]*AgentService, error)
}

// HealthAPI
type HealthAPI interface {
	Service(service, tag string, passingOnly bool, q *QueryOptions) ([]*ServiceEntry, *QueryMeta, error)
}

// WatchPlan
type WatchPlan interface {
	Run(client ClientAPI) error
	SetHandler(handler WatchHandlerFunc) error
	IsStopped() bool
	Stop()
}

// Client
type Client struct {
	*consul.Client
}

// Raw
func (c *Client) Raw() *consul.Client {
	return c.Client
}

// KV
func (c *Client) KV() KvAPI {
	return c.Client.KV()
}

// Session
func (c *Client) Session() SessionAPI {
	return c.Client.Session()
}

// Agent
func (c *Client) Agent() AgentAPI {
	return c.Client.Agent()
}

// Health
func (c *Client) Health() HealthAPI {
	return c.Client.Health()
}

// NewClientWithRaw
func NewClientWithRaw(raw *consul.Client) *Client {
	return &Client{
		Client: raw,
	}
}

// WatchPlanWrapper
type WatchPlanWrapper struct {
	*watch.Plan
}

// Run
func (p *WatchPlanWrapper) Run(client ClientAPI) error {
	writer := logging.GetStdWriter()
	return p.Plan.RunWithClientAndLogger(client.Raw(), log.New(writer, "", log.LstdFlags))
}

// SetHandler
func (p *WatchPlanWrapper) SetHandler(handler WatchHandlerFunc) error {
	if p.Plan.Handler != nil {
		return define.ErrOperationForbidden
	}
	p.Plan.Handler = handler
	return nil
}

// NewWatchPlanWrapper
func NewWatchPlanWrapper(plan *watch.Plan) *WatchPlanWrapper {
	return &WatchPlanWrapper{
		Plan: plan,
	}
}

// WatchPlanParseExempt
func WatchPlanParseExempt(params map[string]interface{}, exempt []string) (*WatchPlanWrapper, error) {
	plan, err := watch.ParseExempt(params, exempt)
	if err != nil {
		return nil, err
	}
	return NewWatchPlanWrapper(plan), nil
}

// ServicePlugin
type ServicePlugin interface {
	define.Service

	Wrap(define.Service) error
	Root() define.Service
}

// ShadowCopier
type ShadowCopier interface {
	define.Task

	Link(source, target, session string) bool
	IsLink(source, target string) bool
	Unlink(source, target string) bool
	Clear()

	Each(fn func(source, target string, info *ShadowInfo) bool)
	Sync(source string, target string) error
	SyncAll() error
}

// NewConsulClient
func NewConsulAPI(config *Config) (ClientAPI, error) {
	client, err := consul.NewClient(config)
	return NewClientWithRaw(client), err
}

// DispatchConverter :
type DispatchConverter interface {
	ElementCreator(element *KVPair) ([]define.IDer, error)
	NodeCreator(node *define.ServiceInfo) (define.IDer, error)
	ShadowCreator(node define.IDer, item define.IDer) (source, target, service string, err error)
	ShadowDetector(pair *consul.KVPair) (source, target, service string, err error)
}

// NewDefaultConsulConfig
func NewDefaultConsulConfig() *Config {
	cfg := consul.DefaultConfig()
	if cfg.HttpAuth == nil {
		cfg.HttpAuth = new(consul.HttpBasicAuth)
	}

	return cfg
}

// NewConsulConfigFromConfig
func NewConsulConfigFromConfig(conf define.Configuration) *Config {
	cfg := NewDefaultConsulConfig()

	cfg.Address = fmt.Sprintf("%s:%d", conf.GetString(ConfKeyHost), conf.GetInt(ConfKeyPort))
	cfg.Scheme = conf.GetString(ConfKeySchema)
	cfg.HttpAuth.Username = conf.GetString(ConfKeyAuthBasicUser)
	cfg.HttpAuth.Password = conf.GetString(ConfKeyAuthBasicPassword)
	cfg.Token = conf.GetString(ConfKeyAuthACLToken)
	cfg.TLSConfig.Address = conf.GetString(ConfKeyTLSServer)
	cfg.TLSConfig.InsecureSkipVerify = conf.GetBool(ConfKeyTLSVerify)
	cfg.TLSConfig.KeyFile = conf.GetString(ConfKeyTLSKeyFile)
	cfg.TLSConfig.CertFile = conf.GetString(ConfKeyTLSCertFile)
	cfg.TLSConfig.CAFile = conf.GetString(ConfKeyTLSCAFile)

	return cfg
}

// NewConsulAPIFromConfig
func NewConsulAPIFromConfig(conf define.Configuration) (ClientAPI, error) {
	return NewConsulAPI(NewConsulConfigFromConfig(conf))
}

//go:generate mockgen -package=${GOPACKAGE}_test -destination=mock_interface_test.go transfer/consul ClientAPI,KvAPI,SessionAPI,AgentAPI,HealthAPI,WatchPlan,ServicePlugin,DispatchConverter,ShadowCopier

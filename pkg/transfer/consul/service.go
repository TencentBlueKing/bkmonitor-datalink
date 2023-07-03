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
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/asaskevich/EventBus"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	MetaSessionLeaderKey = "leader"
)

// TTLDelta
const TTLDelta = 3

const (
	EvHeartBeat        = "heartbeat"
	EvPromoted         = "elect-promoted"
	EvRetired          = "elect-retired"
	EvSessionPreOpen   = "session-pre-open"
	EvSessionPostOpen  = "session-post-open"
	EvServicePostStart = "service-post-start"
	EvEnable           = "service-enable"
	EvDisable          = "service-disable"
)

// ServiceConfig
type ServiceConfig struct {
	ID              string
	Name            string
	Tags            []string
	Address         string
	Port            int
	Meta            map[string]string
	TTL             time.Duration
	SessionBehavior string
	Namespace       string
	ClusterTag      string
}

// Service
type Service struct {
	*ServiceConfig
	Bus                  EventBus.Bus
	ctx                  context.Context
	heartbeatTask        *define.PeriodTask
	monitorSessionCommit monitor.CounterMixin
	monitorElectLeader   monitor.CounterMixin
	session              *Session
	client               ClientAPI
	isLeader             bool
}

// NewService
func NewService(ctx context.Context, client ClientAPI, config *ServiceConfig) *Service {
	labels := prometheus.Labels{
		"name": config.ID,
		"type": "session",
	}

	if config.Tags == nil || len(config.Tags) == 0 {
		config.Tags = append(config.Tags, "service")
	}

	bus := EventBus.New()
	return &Service{
		ctx:           ctx,
		ServiceConfig: config,
		client:        client,
		Bus:           bus,
		heartbeatTask: define.NewPeriodTaskWithEventBus(ctx, config.TTL/TTLDelta, true, EvHeartBeat, bus),
		monitorSessionCommit: monitor.CounterMixin{
			CounterSuccesses: MonitorHeartBeatSuccess.With(labels),
			CounterFails:     MonitorHeartBeatFailed.With(labels),
		},
		monitorElectLeader: monitor.CounterMixin{
			CounterSuccesses: MonitorElectSuccess.With(labels),
			CounterFails:     MonitorElectFailed.With(labels),
		},
		session: NewTransactionalSession(ctx, client, SessionConfig{
			Name:      config.ID,
			TTL:       config.TTL.String(),
			Behavior:  config.SessionBehavior,
			Namespace: utils.ResolveUnixPaths(config.Namespace, "session", config.ID),
		}),
	}
}

// String
func (s *Service) String() string {
	return fmt.Sprintf("%v[%s]", s.Name, s.ID)
}

func (s *Service) setupSession() error {
	session := s.session
	s.Bus.Publish(EvSessionPreOpen)
	err := session.Open()
	if err != nil {
		return err
	}
	logging.Infof("service %v created service session %v", s, session)
	s.Bus.Publish(EvSessionPostOpen)
	fails := 0

	return s.Bus.SubscribeAsync(EvHeartBeat, func(ctx context.Context) {
		reOpen, err := session.CommitReOpen()
		if err != nil {
			fails++
			s.monitorSessionCommit.CounterFails.Inc()
			if fails > TTLDelta {
				logging.Fatalf("commit consul service session %v of %v error %v over %d times, process exit", session, s, err, fails)
			}
			logging.Errorf("commit consul service session %v of %v error %v", session, s, err)
		} else {
			fails = 0
			s.monitorSessionCommit.CounterSuccesses.Inc()
		}

		if reOpen {
			s.Active()
		}
	}, false)
}

// MetaKey
func (s *Service) MetaKey(key string, useKey bool) string {
	if useKey {
		return utils.ResolveUnixPaths("", key, "meta", MetaSessionLeaderKey)
	}
	return utils.ResolveUnixPaths("", s.Namespace, "meta", MetaSessionLeaderKey)
}

// ElectLeader
func (s *Service) ElectLeader() error {
	payload, err := json.Marshal(&define.ServiceInfo{
		ID:      s.ID,
		Address: s.Address,
		Port:    s.Port,
		Tags:    s.Tags,
		Meta:    s.Meta,
	})
	if err != nil {
		return err
	}
	err = s.session.Set(s.MetaKey(MetaSessionLeaderKey, false), payload, define.StoreNoExpires)
	switch err {
	case define.ErrItemAlreadyExists:
		if s.isLeader {
			logging.Infof("service %v retired", s)
			s.Bus.Publish(EvRetired, s.ID)
		}
		s.isLeader = false
		s.monitorElectLeader.CounterFails.Inc()
		return nil
	case nil:
		if !s.isLeader {
			logging.Infof("service %v become leader", s)
			s.Bus.Publish(EvPromoted, s.ID)
		}
		s.isLeader = true
		s.monitorElectLeader.CounterSuccesses.Inc()
	default:
		logging.Errorf("elect leader failed: %v", err)
	}
	return err
}

func (s *Service) Active() {
	s.Bus.Publish(EvServicePostStart)
}

// Start
func (s *Service) Start() error {
	logging.Infof("service %s registered", s)
	err := s.setupSession()
	if err != nil {
		return err
	}
	logging.Infof("service %v session opened", s)

	err = s.heartbeatTask.Start()
	if err != nil {
		return err
	}
	logging.Infof("service %v heartbeat started", s)
	s.Active()
	return nil
}

// Stop
func (s *Service) Stop() error {
	logging.Infof("service %v stop heartbeatTask", s)
	err := s.heartbeatTask.Stop()
	if err != nil {
		return err
	}

	session := s.session
	logging.Infof("service %v close service session %v", s, session)
	err = session.Close()
	if err != nil {
		return err
	}

	logging.Infof("service %v stopped", s)
	return nil
}

// Wait
func (s *Service) Wait() error {
	return s.heartbeatTask.Wait()
}

// Session
func (s *Service) Session() define.Session {
	return s.session
}

// Enable
func (s *Service) Enable() error {
	logging.Infof("service %v enabled", s)
	s.Bus.Publish(EvEnable)
	return nil
}

// Disable
func (s *Service) Disable() error {
	logging.Warnf("service %v disabled", s)
	s.Bus.Publish(EvDisable)
	return nil
}

func (s *Service) EventBus() EventBus.Bus {
	return s.Bus
}

// Info
func (s *Service) Info(t define.ServiceType) ([]*define.ServiceInfo, error) {
	infos := make([]*define.ServiceInfo, 0, 1)
	switch t {
	case define.ServiceTypeMe:
		info, err := s.Service(s.ID)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	case define.ServiceTypeLeader:
		info, err := s.Leader()
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	case define.ServiceTypeAll:
		return s.Family()
	case define.ServiceTypeClusterAll:
		return s.All()
	case define.ServiceTypeLeaderAll:
		return s.AllLeader()
	default:
		return nil, define.ErrType
	}

	return infos, nil
}

// Leader : get service api added after consul 1.3.0
func (s *Service) Leader() (*define.ServiceInfo, error) {
	payload, err := s.session.Get(s.MetaKey(MetaSessionLeaderKey, false))
	if err != nil {
		return nil, err
	}
	var info define.ServiceInfo
	err = json.Unmarshal(payload, &info)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// Family
func (s *Service) Family() ([]*define.ServiceInfo, error) {
	serviceIDCluster, _, err := s.getClusters()
	if err != nil {
		return nil, err
	}

	// 只获取当前集群
	clusterID := config.Configuration.GetString(ConfKeyClusterID)
	return s.instances(clusterID, serviceIDCluster)
}

// All transfer
func (s *Service) All() ([]*define.ServiceInfo, error) {
	var infos []*define.ServiceInfo

	serviceIDCluster, _, err := s.getClusters()
	if err != nil {
		return nil, err
	}

	// 获取所有集群
	info, err := s.instances("", serviceIDCluster)
	if err != nil {
		return nil, err
	}
	logging.Debugf("get info [%#v]", info)
	infos = append(infos, info...)

	return infos, nil
}

// all leaders
func (s *Service) AllLeader() ([]*define.ServiceInfo, error) {
	var infos []*define.ServiceInfo
	var lasterr error

	_, clusters, err := s.getClusters()
	if err != nil {
		return nil, err
	}

	servicePrefix, _ := filepath.Split(strings.Trim(s.Namespace, "/"))
	for cName := range clusters {
		leaderPath := s.MetaKey(servicePrefix+cName, true)
		logging.Infof("get leader from %s", leaderPath)
		payload, err := s.session.Get(leaderPath)
		if err != nil {
			logging.Warnf("get leader from %s error: %s", leaderPath, err)
			continue
		}
		var info define.ServiceInfo
		err = json.Unmarshal(payload, &info)
		if err != nil {
			logging.Warnf("unmarshal service: [%v] error: %s", payload, err)
			lasterr = err
		}
		if info.Meta == nil {
			info.Meta = make(map[string]string, 0)
		}
		// 0.7.5版本不支持meta，使用  service_name-{hash值} 获取service_name
		serviceArr := strings.Split(info.ID, "-")
		info.Meta["service"] = strings.Join(serviceArr[:len(serviceArr)-1], "")
		info.Meta["cluster_id"] = cName
		infos = append(infos, &info)
	}
	return infos, lasterr
}

func (s *Service) Service(id string) (*define.ServiceInfo, error) {
	serviceIDCluster, _, err := s.getClusters()
	if err != nil {
		return nil, err
	}

	for serviceID := range serviceIDCluster {
		if serviceID == id {
			return &define.ServiceInfo{
				ID:      id,
				Address: s.Address,
				Port:    s.Port,
				Tags:    s.Tags,
				Meta:    s.Meta,
			}, nil
		}
	}
	return nil, fmt.Errorf("unknown service id %v", id)
}

func (s *Service) getClusters() (map[string]string, map[string]struct{}, error) {
	serviceIDCluster := make(map[string]string) // map[serviceID]clusterID
	clusters := make(map[string]struct{})

	// 获取所有的 clusterID
	//
	// $ consul kv get --keys bk_bkmonitorv3_enterprise_production/service/v1/
	// bk_bkmonitorv3_enterprise_production/service/v1/debug/
	// bk_bkmonitorv3_enterprise_production/service/v1/default/
	//
	keys, _, err := s.client.KV().Keys(define.ConfRootV1+"/", "/", NewQueryOptions(s.ctx))
	if err != nil {
		return nil, nil, err
	}
	logging.Infof("list clusters found cluster count %d", len(keys))

	for _, key := range keys {
		logging.Infof("list cluster key %s", key)
		items := strings.Split(strings.TrimSuffix(key, "/"), "/")
		if len(items) <= 0 {
			logging.Errorf("invalid root key %s", key)
			continue
		}
		clusterID := items[len(items)-1]
		clusters[clusterID] = struct{}{}
	}

	for cluster := range clusters {
		// 获取 session 列表 拿到 serviceID
		//
		// $consul kv get --keys bk_bkmonitorv3_enterprise_production/service/v1/default/session/
		// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/
		//
		sessionKey := path.Join(define.ConfRootV1, cluster, "session")
		logging.Infof("list session key %s", sessionKey)
		keys, _, err := s.client.KV().Keys(sessionKey+"/", "/", NewQueryOptions(s.ctx))
		if err != nil {
			return nil, nil, err
		}

		for _, key := range keys {
			items := strings.Split(strings.TrimSuffix(key, "/"), "/")
			if len(items) <= 0 {
				logging.Errorf("invalid session key %s", key)
				continue
			}
			serviceId := items[len(items)-1]
			serviceIDCluster[serviceId] = cluster
			logging.Infof("list service key '%v', serviceName=%v, serviceID=%v, cluster=%v", key, s.Name, serviceId, cluster)
		}
	}

	return serviceIDCluster, clusters, nil
}

func extractSessionDetailed(kvs KVPairs) (*define.ServiceInfo, error) {
	info := &define.ServiceInfo{}
	for _, kv := range kvs {
		if strings.HasSuffix(kv.Key, "/service_id") {
			info.ID = string(kv.Value)
		}

		if strings.HasSuffix(kv.Key, "/service_host") {
			info.Address = string(kv.Value)
		}

		if strings.HasSuffix(kv.Key, "/service_port") {
			port, err := strconv.Atoi(string(kv.Value))
			if err != nil {
				return nil, err
			}
			info.Port = port
		}

		if strings.HasSuffix(kv.Key, "/service_tag") {
			info.Tags = strings.Split(string(kv.Value), ",")
		}
	}
	return info, nil
}

// instances 根据 serviceName, clusterID 获取 transfer 实例信息
// serviceIDCluster => map[serviceID]clusterID
func (s *Service) instances(wantClusterID string, serviceIDCluster map[string]string) ([]*define.ServiceInfo, error) {
	logging.Infof("list %s all service by clusterID [%s]", s.Name, wantClusterID)
	serviceInfos := make([]*define.ServiceInfo, 0)
	for serviceID, clusterID := range serviceIDCluster {
		if wantClusterID != "" && wantClusterID != clusterID {
			continue
		}
		// 获取 session values
		// $ consul kv get --keys  bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/
		//
		// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/client_id
		// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/service_id
		// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/service_name
		// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/version
		// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/service_host
		// bk_bkmonitorv3_enterprise_production/service/v1/default/session/bkmonitorv3-2604497288/service_port
		//
		prefix := path.Join(define.ConfRootV1, clusterID, "session", serviceID)
		logging.Infof("list instance key %s for service %s", prefix, serviceID)
		kvs, _, err := s.client.KV().List(prefix+"/", NewQueryOptions(s.ctx))
		if err != nil {
			return nil, err
		}

		info, err := extractSessionDetailed(kvs)
		if err != nil {
			logging.Errorf("failed to extract service info from session: %v", err)
			continue
		}
		info.Meta = map[string]string{"cluster_id": clusterID}
		serviceInfos = append(serviceInfos, info)
	}

	return serviceInfos, nil
}

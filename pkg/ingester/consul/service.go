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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	consul "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

const (
	MetaSessionLeaderKey = "leader"
)

// Service
type Service struct {
	define.ServiceInfo
	TTL                 string
	SessionBehavior     string
	client              *consul.Client
	isLeader            bool
	heartbeatTicker     *time.Ticker
	heartbeatTickerDone chan bool
	sessionID           string
	check               *consul.AgentServiceCheck
}

// NewService
func NewService(tags []string) (*Service, error) {
	client, err := consul.NewClient(NewConfig())
	if err != nil {
		return nil, err
	}
	tag := config.Configuration.Consul.ServiceTag

	service := &Service{
		ServiceInfo: define.ServiceInfo{
			ID:      define.ServiceID,
			Name:    config.Configuration.Consul.ServiceName,
			Address: config.Configuration.Http.Host,
			Port:    config.Configuration.Http.Port,
			Tags:    append(tags, tag+"-service", tag),
			Meta: map[string]string{
				"version": define.Version,
				"pid":     strconv.Itoa(os.Getpid()),
				"service": config.Configuration.Consul.ServiceName,
				"module":  config.Configuration.Consul.ServiceTag,
			},
		},
		SessionBehavior: consul.SessionBehaviorDelete,
		TTL:             config.Configuration.Consul.ClientTTL,
		client:          client,
	}

	service.check = &consul.AgentServiceCheck{
		CheckID: service.ID,
		Name:    "Process Heartbeat",
		TTL:     service.TTL,
	}

	return service, nil
}

// String
func (s *Service) String() string {
	return fmt.Sprintf("%v[%s]", s.Name, s.ID)
}

// Start
func (s *Service) Start() error {
	var err error

	err = s.registerService()
	if err != nil {
		return errors.WithStack(err)
	}

	err = s.startHeartbeat()
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Stop
func (s *Service) Stop() error {
	var err error

	err = s.destroySession()
	if err != nil {
		return errors.WithStack(err)
	}

	s.stopHeartbeat()

	err = s.deregisterService()
	if err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// registerService: 注册服务到Consul
func (s *Service) registerService() error {
	logger := logging.GetLogger()

	logger.Infof("start to register service %v", s.ServiceInfo)

	// 配置存活检查
	agent := s.client.Agent()

	err := agent.ServiceRegister(&consul.AgentServiceRegistration{
		ID:      s.ID,
		Name:    s.Name,
		Tags:    s.Tags,
		Address: s.Address,
		Port:    s.Port,
		Meta:    s.Meta,
		Check:   s.check,
	})
	if err != nil {
		logger.Errorf("service %v registere error %v", s.ServiceInfo, err)
		return errors.WithStack(err)
	}
	logger.Infof("service %s registered, full config %v", s, s.ServiceInfo)
	return nil
}

// deregisterService: 从Consul注销服务
func (s *Service) deregisterService() error {
	logger := logging.GetLogger()
	agent := s.client.Agent()
	logger.Infof("service %v deregister", s)
	err := agent.ServiceDeregister(s.ID)
	if err != nil {
		return errors.WithStack(err)
	}

	logger.Infof("service %v stopped", s)
	return nil
}

// startHeartbeat: 启动心跳上报
func (s *Service) startHeartbeat() error {
	if s.heartbeatTicker != nil {
		return nil
	}

	logger := logging.GetLogger()
	// 启动 ticker
	reportDuration, err := time.ParseDuration(s.TTL)
	if err != nil {
		return err
	}
	// 取过期时间的一半作为心跳上报时间
	s.heartbeatTicker = time.NewTicker(reportDuration / 2)
	s.heartbeatTickerDone = make(chan bool)

	// 周期开始之前，先立即来一发
	s.PeriodicCheck()

	go func() {
		for {
			select {
			case <-s.heartbeatTickerDone:
				logger.Infof("service %s heartbeat check stopped", s)
				return
			case <-s.heartbeatTicker.C:
				s.PeriodicCheck()
			}
		}
	}()

	logger.Infof("service %s heartbeat check started", s)

	return nil
}

func (s *Service) PeriodicCheck() {
	logger := logging.GetLogger()

	var err error
	agent := s.client.Agent()

	err = agent.UpdateTTL(s.ID, time.Now().String(), consul.HealthPassing)
	if err != nil {
		logger.Errorf("service %v heartbeat check failed: %s", s, err)
	}

	err = s.ensureSession()
	if err != nil {
		logger.Errorf("service %s session renew field: %s", s, err)
	}

	err = s.ElectLeader()
	if err != nil {
		logger.Errorf("service %s elect leader failed: %s", s, err)
	}

	if s.isLeader {
		// 如果是leader，需要进行DataID的分配
		dispatcher, err := NewDispatcher()
		if err != nil {
			logger.Errorf("service %s dispatch datasource failed: %s", s, err)
		} else {
			dispatcher.Run()
		}
	}
}

// stopHeartbeat: 停止心跳上报
func (s *Service) stopHeartbeat() {
	if s.heartbeatTicker == nil {
		return
	}

	s.heartbeatTicker.Stop()
	s.heartbeatTicker = nil

	close(s.heartbeatTickerDone)
	s.heartbeatTickerDone = nil
}

func (s *Service) ensureSession() error {
	logger := logging.GetLogger()

	if s.sessionID == "" {
		return s.createSession()
	}
	oldSessionID := s.sessionID
	session := s.client.Session()

	var err error
	entry, _, _ := session.Info(s.sessionID, nil)
	if entry != nil {
		_, _, err = session.Renew(s.sessionID, nil)
		if err != nil {
			return errors.WithStack(err)
		}
		return nil
	}
	err = s.createSession()
	if err != nil {
		return errors.WithStack(err)
	}
	logger.Infof("service %v session invalid, create new one: (%s) -> (%s)", s, oldSessionID, s.sessionID)
	return nil
}

func (s *Service) createSession() error {
	logger := logging.GetLogger()

	sessionID, _, err := s.client.Session().Create(&consul.SessionEntry{
		Name:      s.ID,
		TTL:       s.TTL,
		Behavior:  s.SessionBehavior,
		LockDelay: time.Second,
	}, nil)
	if err != nil {
		return errors.WithStack(err)
	}
	s.sessionID = sessionID
	logger.Infof("service %s session created: SessionID(%s)", s, s.sessionID)
	return nil
}

func (s *Service) destroySession() error {
	if s.sessionID == "" {
		return nil
	}

	logger := logging.GetLogger()

	_, err := s.client.Session().Destroy(s.sessionID, nil)
	if err != nil {
		return errors.WithStack(err)
	}
	logger.Infof("service %s session destroyed: SessionID(%s)", s, s.sessionID)
	s.sessionID = ""
	return nil
}

func (s *Service) ElectLeader() error {
	logger := logging.GetLogger()

	payload, err := json.Marshal(&s.ServiceInfo)
	if err != nil {
		return errors.WithStack(err)
	}

	ok, _, err := s.client.KV().Acquire(&consul.KVPair{
		Key:     utils.ResolveUnixPath(config.Configuration.Consul.ServicePath, MetaSessionLeaderKey),
		Value:   payload,
		Session: s.sessionID,
	}, nil)
	if err != nil {
		return errors.WithStack(err)
	}

	if ok {
		if !s.isLeader {
			logger.Infof("service %v become leader", s)
		}
		s.isLeader = true
	} else {
		if s.isLeader {
			logger.Infof("service %v retired leader", s)
		}
		s.isLeader = false
	}

	return nil
}

func ListServices(passingOnly bool) ([]*consul.ServiceEntry, error) {
	client, err := consul.NewClient(NewConfig())
	if err != nil {
		return nil, err
	}

	health := client.Health()
	serviceEntries, _, err := health.Service(
		config.Configuration.Consul.ServiceName, config.Configuration.Consul.ServiceTag, passingOnly, nil)
	if err != nil {
		return nil, err
	}

	return serviceEntries, err
}

func GetLeader() (*define.ServiceInfo, error) {
	client, err := consul.NewClient(NewConfig())
	if err != nil {
		return nil, err
	}
	key := utils.ResolveUnixPath(config.Configuration.Consul.ServicePath, MetaSessionLeaderKey)
	kvPair, _, err := client.KV().Get(key, nil)
	if err != nil {
		return nil, err
	}
	serviceInfo := &define.ServiceInfo{}
	if kvPair == nil {
		return serviceInfo, nil
	}

	err = json.Unmarshal(kvPair.Value, serviceInfo)
	if err != nil {
		return nil, err
	}
	return serviceInfo, nil
}

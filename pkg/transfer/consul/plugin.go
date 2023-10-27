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
	"sync/atomic"

	consul "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// GetRootService
var GetRootService = func(service define.Service) (*Service, error) {
	switch s := service.(type) {
	case *Service:
		return s, nil
	case ServicePlugin:
		root := s.Root()
		rootService, ok := root.(*Service)
		if !ok {
			return nil, errors.WithMessagef(define.ErrType, "%T not supported", rootService)
		}
		return rootService, nil
	default:
		return nil, errors.WithMessagef(define.ErrType, "%T not supported", s)
	}
}

// GetServiceEventBus
var GetServiceEventBus = func(service define.Service) (eventbus.Bus, error) {
	root, err := GetRootService(service)
	if err != nil {
		return nil, err
	}

	return root.Bus, nil
}

// GetServiceConfig
var GetServiceConfig = func(service define.Service) (*ServiceConfig, error) {
	root, err := GetRootService(service)
	if err != nil {
		return nil, err
	}

	return root.ServiceConfig, nil
}

// GetServiceClient
var GetServiceClient = func(service define.Service) (ClientAPI, error) {
	root, err := GetRootService(service)
	if err != nil {
		return nil, err
	}

	return root.client, nil
}

// GetServiceContext
var GetServiceContext = func(service define.Service) (context.Context, error) {
	root, err := GetRootService(service)
	if err != nil {
		return nil, err
	}

	return root.ctx, nil
}

// BaseServicePlugin
type BaseServicePlugin struct {
	define.Service
	root define.Service
}

// Root
func (p *BaseServicePlugin) Root() define.Service {
	return p.root
}

// Wrap
func (p *BaseServicePlugin) Wrap(service define.Service) error {
	root, err := GetRootService(service)
	if err != nil {
		return err
	}
	p.root = root
	p.Service = service
	return nil
}

// NewBaseServicePlugin
func NewBaseServicePlugin() *BaseServicePlugin {
	return &BaseServicePlugin{}
}

// MetaInfoPlugin
type MetaInfoPlugin struct {
	*BaseServicePlugin
}

// Wrap
func (p *MetaInfoPlugin) Wrap(service define.Service) error {
	err := p.BaseServicePlugin.Wrap(service)
	if err != nil {
		return err
	}

	root := p.root

	bus, err := GetServiceEventBus(root)
	if err != nil {
		return err
	}

	conf, err := GetServiceConfig(root)
	if err != nil {
		return err
	}

	return bus.SubscribeAsync(EvServicePostStart, func() {
		session := p.root.Session()

		meta := map[string]string{
			"service_id":   conf.ID,
			"service_name": conf.Name,
			"service_host": conf.Address,
			"service_port": fmt.Sprintf("%d", conf.Port),
			"service_tag":  strings.Join(conf.Tags, ","),
			"client_id":    define.ProcessID,
			"version":      define.Version,
		}

		ctx, err := GetServiceContext(root)
		if err != nil {
			logging.Errorf("get context from service error %v, set up meta data failed", err)
			return
		}

		for key, value := range meta {
			go func(key, value string) {
				defer utils.RecoverError(func(e error) {
					logging.Errorf("set meta %s panic %v", key, err)
				})

				err := utils.ContextExponentialRetry(ctx, func() error {
					return session.Set(key, []byte(value), define.StoreNoExpires)
				})
				if err != nil {
					logging.Warnf("set meta %v error %v", key, err)
				}
			}(key, value)
		}
	}, false)
}

// NewMetaInfoPlugin
func NewMetaInfoPlugin() *MetaInfoPlugin {
	return &MetaInfoPlugin{
		BaseServicePlugin: NewBaseServicePlugin(),
	}
}

// ElectionPlugin
type ElectionPlugin struct {
	*BaseServicePlugin
}

// Wrap
func (p *ElectionPlugin) Wrap(service define.Service) error {
	err := p.BaseServicePlugin.Wrap(service)
	if err != nil {
		return err
	}

	root, err := GetRootService(p.root)
	if err != nil {
		return err
	}

	bus, err := GetServiceEventBus(root)
	if err != nil {
		return err
	}
	return bus.SubscribeAsync(EvHeartBeat, func(ctx context.Context) {
		err := root.ElectLeader()
		if err != nil {
			logging.Errorf("service %v elect failed %v", service, err)
		}
	}, false)
}

// NewElectionPlugin
func NewElectionPlugin() *ElectionPlugin {
	return &ElectionPlugin{
		BaseServicePlugin: NewBaseServicePlugin(),
	}
}

// TTLCheckPlugin
type TTLCheckPlugin struct {
	*BaseServicePlugin
	status            atomic.Value
	monitorServiceTTL monitor.CounterMixin
}

func (p *TTLCheckPlugin) setupStatus() error {
	root := p.root
	p.status.Store(consul.HealthPassing)

	bus, err := GetServiceEventBus(root)
	if err != nil {
		return err
	}

	err = bus.Subscribe(EvEnable, func() {
		p.status.Store(consul.HealthPassing)
	})
	if err != nil {
		return err
	}

	err = bus.Subscribe(EvDisable, func() {
		p.status.Store(consul.HealthCritical)
	})
	if err != nil {
		return err
	}

	return nil
}

func (p *TTLCheckPlugin) setupMonitor() error {
	root := p.root
	conf, err := GetServiceConfig(root)
	if err != nil {
		return err
	}

	labels := prometheus.Labels{
		"name": conf.ID,
		"type": "service",
	}
	p.monitorServiceTTL = monitor.CounterMixin{
		CounterSuccesses: MonitorHeartBeatSuccess.With(labels),
		CounterFails:     MonitorHeartBeatFailed.With(labels),
	}
	return nil
}

// Wrap
func (p *TTLCheckPlugin) Wrap(service define.Service) error {
	err := p.BaseServicePlugin.Wrap(service)
	if err != nil {
		return err
	}
	wrapper := []func() error{
		p.setupMonitor,
		p.setupStatus,
	}

	for _, fn := range wrapper {
		err := fn()
		if err != nil {
			return err
		}
	}

	return nil
}

// NewTTLCheckPlugin
func NewTTLCheckPlugin() *TTLCheckPlugin {
	return &TTLCheckPlugin{
		BaseServicePlugin: NewBaseServicePlugin(),
	}
}

// MaintenancePlugin
type MaintenancePlugin struct {
	*BaseServicePlugin
}

// Wrap
func (p *MaintenancePlugin) Wrap(service define.Service) error {
	err := p.BaseServicePlugin.Wrap(service)
	if err != nil {
		return err
	}

	root := p.root
	client, err := GetServiceClient(root)
	if err != nil {
		return err
	}

	conf, err := GetServiceConfig(root)
	if err != nil {
		return err
	}

	agent := client.Agent()
	bus, err := GetServiceEventBus(p.root)
	if err != nil {
		return err
	}

	err = bus.Subscribe(EvEnable, func() {
		err := agent.DisableServiceMaintenance(conf.ID)
		if err != nil {
			logging.Errorf("disable service %v maintenance error %v", root, err)
		}
	})
	if err != nil {
		return err
	}

	err = bus.Subscribe(EvDisable, func() {
		err := agent.EnableServiceMaintenance(conf.ID, "")
		if err != nil {
			logging.Errorf("enable service %v maintenance error %v", root, err)
		}
	})
	if err != nil {
		return err
	}

	return nil
}

// NewMaintenancePlugin
func NewMaintenancePlugin() *MaintenancePlugin {
	return &MaintenancePlugin{
		BaseServicePlugin: NewBaseServicePlugin(),
	}
}

// LeaderMixin
type LeaderMixin struct {
	ctx        context.Context
	promotedFn func(ctx context.Context) error
}

// Wrap
func (l *LeaderMixin) Wrap(service define.Service) error {
	root, err := GetRootService(service)
	if err != nil {
		return err
	}

	bus, err := GetServiceEventBus(service)
	if err != nil {
		return err
	}

	ch := make(chan context.CancelFunc, 1)
	err = bus.SubscribeAsync(EvPromoted, func(id string) {
		ctx, cancel := context.WithCancel(root.ctx)
		select {
		case ch <- cancel:
			err := l.promotedFn(ctx)
			if err != nil {
				logging.Fatalf("promote leader error %v", err)
			}
		default:
			cancel()
		}
	}, false)
	if err != nil {
		return err
	}

	return bus.SubscribeAsync(EvRetired, func(id string) {
	loop:
		for {
			select {
			case cancel := <-ch:
				cancel()
			default:
				break loop
			}
		}
	}, false)
}

// NewLeaderMixin
func NewLeaderMixin(ctx context.Context, promotedFn func(ctx context.Context) error) *LeaderMixin {
	return &LeaderMixin{
		ctx:        ctx,
		promotedFn: promotedFn,
	}
}

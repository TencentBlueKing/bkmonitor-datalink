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
	"strings"

	consul "github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type Dispatcher struct {
	DataSources           []*define.DataSourceKVPair
	Services              []*define.ServiceInfo
	DispatchedDataSources []*define.DataSourceKVPair
	client                *consul.Client
}

type ServiceDispatchPlan map[string][]*define.DataSourceKVPair

func (d *Dispatcher) getDataSourceMappings() map[int]*define.DataSourceKVPair {
	mappings := make(map[int]*define.DataSourceKVPair)
	for _, pair := range d.DataSources {
		mappings[pair.DataSource.DataID] = pair
	}
	return mappings
}

func (d *Dispatcher) getServiceMappings() map[int]*define.ServiceInfo {
	mappings := make(map[int]*define.ServiceInfo)
	for _, service := range d.Services {
		mappings[utils.HashItInt(service.ID)] = service
	}
	return mappings
}

func (d *Dispatcher) splitDataSourceByMode() ([]*define.DataSourceKVPair, []*define.DataSourceKVPair) {
	var receivers []*define.DataSourceKVPair
	var pollers []*define.DataSourceKVPair

	for _, pair := range d.DataSources {
		runMode := pair.DataSource.MustGetPluginOption().GetRunMode()
		if runMode == define.PluginRunModePull {
			pollers = append(pollers, pair)
		} else if runMode == define.PluginRunModePush {
			receivers = append(receivers, pair)
		}
	}
	return receivers, pollers
}

// GetPlan: 生成DataID分配计划
func (d *Dispatcher) GetPlan() ServiceDispatchPlan {
	logger := logging.GetLogger()

	dsMappings := d.getDataSourceMappings()
	serviceMappings := d.getServiceMappings()

	receivers, pollers := d.splitDataSourceByMode()

	var elements []utils.IDer
	// 只需要对拉取类的DataID进行哈希分配
	for _, pair := range pollers {
		elements = append(elements, utils.NewDetailsBalanceElement(pair, pair.DataSource.DataID))
	}

	var nodes []utils.IDer
	for _, service := range d.Services {
		nodes = append(nodes, utils.NewDetailsBalanceElement(service, utils.HashItInt(service.ID)))
	}

	// 使用hash环算法均分elements到nodes中
	mappings := utils.Balance(elements, nodes)

	plan := make(ServiceDispatchPlan)

	for node, els := range mappings {
		service, ok := serviceMappings[node.ID()]
		if !ok {
			logger.Fatalf("service %d not found", node.ID())
			continue
		}

		var dataSources []*define.DataSourceKVPair
		// 遍历element，配合node生成shadow信息
		for _, element := range els {
			dataSource, ok := dsMappings[element.ID()]
			if !ok {
				logger.Fatalf("dataSource %d not found", element.ID())
				continue
			}

			dataSources = append(dataSources, dataSource)
		}

		plan[service.ID] = append(dataSources, receivers...)
	}
	return plan
}

func (d *Dispatcher) GetOldPlan() ServiceDispatchPlan {
	dispatched := make(ServiceDispatchPlan)
	for _, pair := range d.DispatchedDataSources {
		paths := strings.Split(strings.Trim(pair.Pair.Key, "/"), "/")
		if len(paths) < 2 {
			continue
		}
		// 路径的倒数第二个是服务ID
		service := paths[len(paths)-2]
		dispatched[service] = append(dispatched[service], pair)
	}
	return dispatched
}

// 将当前分配计划与现有分配进行对比
func (d *Dispatcher) DiffPlan() (planToAdd ServiceDispatchPlan, planToDelete ServiceDispatchPlan) {
	logger := logging.GetLogger()

	plan := d.GetPlan()
	oldPlan := d.GetOldPlan()

	logger.Debugf("datasource dispatch plan diff: [NOW]->%+v, [OLD]->%+v", plan, oldPlan)

	planToAdd = make(ServiceDispatchPlan)
	planToDelete = make(ServiceDispatchPlan)
	// 将计划与当前进行比对
	for service, pairs := range plan {
		oldPairs, ok := oldPlan[service]
		// 如果当前配置是空，则计划新增
		if !ok {
			planToAdd[service] = append(planToAdd[service], pairs...)
			continue
		}

		oldPairsMapping := make(map[int]*define.DataSourceKVPair)

		for _, pair := range oldPairs {
			oldPairsMapping[pair.DataSource.DataID] = pair
		}

		for _, pair := range pairs {
			key := pair.DataSource.DataID
			oldPair, ok := oldPairsMapping[key]

			if ok {
				delete(oldPairsMapping, key)
				if pair.Pair.ModifyIndex == oldPair.Pair.ModifyIndex {
					// 若存在，且不变，则无需更新
					continue
				}
			}

			// 无论是新增还是更新的，都统一视作新增
			planToAdd[service] = append(planToAdd[service], pair)
		}

		// 剩余未匹配上的，需要清理
		for _, pair := range oldPairsMapping {
			planToDelete[service] = append(planToDelete[service], pair)
		}

		delete(oldPlan, service)
	}

	// 剩余未匹配上的，需要清理
	for service, pairs := range oldPlan {
		planToDelete[service] = append(planToDelete[service], pairs...)
	}

	return planToAdd, planToDelete
}

func (d *Dispatcher) Run() {
	logger := logging.GetLogger()

	planToAdd, planToDelete := d.DiffPlan()

	logger.Debugf("datasource dispatch ready: [ADD]->%+v, [DELETE]->%+v", planToAdd, planToDelete)

	successAddPair := make(map[string][]int)
	successDeletePair := make(map[string][]int)

	for service, pairs := range planToAdd {
		for _, pair := range pairs {

			// 对整个kv对进行序列化，目的是将 ModifyIndex 保存下来
			value, err := json.Marshal(pair.Pair)

			if err == nil {
				_, err = d.client.KV().Put(&consul.KVPair{
					Key:         config.Configuration.Consul.GetShadowPath(service, pair.DataSource.DataID),
					Value:       value,
					CreateIndex: pair.Pair.CreateIndex,
					ModifyIndex: pair.Pair.ModifyIndex,
				}, nil)
			}

			if err != nil {
				logger.Errorf("dataSource(%d) plan ADD to service(%s) error: %s", pair.DataSource.DataID, service, err)
				continue
			}

			successAddPair[service] = append(successAddPair[service], pair.DataSource.DataID)
		}
	}

	for service, pairs := range planToDelete {
		for _, pair := range pairs {
			_, err := d.client.KV().Delete(config.Configuration.Consul.GetShadowPath(service, pair.DataSource.DataID), nil)
			if err != nil {
				logger.Errorf("dataSource(%d) plan DELETE to service(%s) error: %s", pair.DataSource.DataID, service, err)
				continue
			}
			successDeletePair[service] = append(successDeletePair[service], pair.DataSource.DataID)
		}
	}

	if len(successAddPair) > 0 || len(successDeletePair) > 0 {
		successAddPairString, _ := json.Marshal(successAddPair)
		successDeletePairString, _ := json.Marshal(successDeletePair)
		logger.Infof("datasource dispatch done: [ADD]->%s, [DELETE]->%s", successAddPairString, successDeletePairString)
	}
}

func NewDispatcher() (*Dispatcher, error) {
	logger := logging.GetLogger()

	var err error

	dispatcher := &Dispatcher{}

	dispatcher.client, err = consul.NewClient(NewConfig())
	if err != nil {
		return nil, err
	}

	var kvPairs consul.KVPairs

	// 获取当前全部的DataID列表
	kvPairs, err = ListDataSources(config.Configuration.Consul.GetDataIDPathPrefix())
	if err != nil {
		return nil, fmt.Errorf("list public data sources error: %+v", err)
	}

	for _, kvPair := range kvPairs {
		ds, err := ParseDataSourceFromKVPair(kvPair)
		if err != nil {
			logger.Debugf("datasource parse error: %+v, origin data: %s", err, kvPair.Value)
			continue
		}
		logger.Debugf("datasource parse success, parse data: %s", ds)
		dispatcher.DataSources = append(dispatcher.DataSources, ds)
	}

	// 获取当前分配的DataID列表
	kvPairs, err = ListDataSources(config.Configuration.Consul.GetShadowPathPrefix())
	if err != nil {
		return nil, fmt.Errorf("list shadow data sources error: %+v", err)
	}

	for _, kvPair := range kvPairs {
		// 需要对value进行额外一次反序列化，才能拿到原有的KV对
		ds, err := ParseDataSourceFromShadowKVPair(kvPair)
		if err != nil {
			logger.Debugf("datasource parse error: %+v, origin data: %s", err, kvPair.Value)
			continue
		}
		dispatcher.DispatchedDataSources = append(dispatcher.DispatchedDataSources, ds)
	}

	services, err := ListServices(true)
	if err != nil {
		return nil, fmt.Errorf("list services error: %+v", err)
	}

	for _, service := range services {
		dispatcher.Services = append(dispatcher.Services, &define.ServiceInfo{
			ID:      service.Service.ID,
			Name:    service.Service.Service,
			Tags:    service.Service.Tags,
			Address: service.Service.Address,
			Port:    service.Service.Port,
			Meta:    service.Service.Meta,
		})
	}

	logger.Debugf("dispatcher init success: datasource(%d), dispatched(%d), service(%d)",
		len(dispatcher.DataSources), len(dispatcher.DispatchedDataSources), len(dispatcher.Services))

	return dispatcher, nil
}

func ListDispatchPlan() (ServiceDispatchPlan, error) {
	logger := logging.GetLogger()

	// 获取当前分配的DataID列表
	kvPairs, err := ListDataSources(config.Configuration.Consul.GetShadowPathPrefix())
	if err != nil {
		return nil, fmt.Errorf("list shadow data sources error: %+v", err)
	}

	plan := ServiceDispatchPlan{}

	for _, kvPair := range kvPairs {
		// 需要对value进行额外一次反序列化，才能拿到原有的KV对
		ds, err := ParseDataSourceFromShadowKVPair(kvPair)
		if err != nil {
			logger.Debugf("datasource parse error: %+v, origin data: %s", err, kvPair.Value)
			continue
		}
		service := GetServiceNameFromShadow(ds.Pair)
		if service == "" {
			continue
		}

		plan[service] = append(plan[service], ds)
	}
	return plan, nil
}

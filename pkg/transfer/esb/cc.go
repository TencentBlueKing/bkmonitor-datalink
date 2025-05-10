// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb

import (
	"context"
	"fmt"

	"github.com/cstockton/go-conv"
	"github.com/dghubble/sling"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	authKey   = "X-Bkapi-Authorization"
	tenantKey = "X-Bk-Tenant-Id"
)

type APIClient interface {
	GetSearchBusiness() ([]CCSearchBusinessResponseInfo, error)
	GetServiceInstance(bkTenantID string, bizID, limit, start int, ServiceInstanceIds []int) (*CCSearchServiceInstanceResponseData, error)
	GetSearchBizInstTopo(bkTenantID string, start, bizID, limit, level int) ([]CCSearchBizInstTopoResponseInfo, error)
	GetHostsByRange(bkTenantID string, bizID, limit, start int) (*CCSearchHostResponseData, error)
	VisitAllHost(ctx context.Context, batchSize int, ccInfo models.CCInfo, fn func(monitor CCSearchHostResponseDataV3Monitor, ccInfo models.CCInfo) error) error
}

// CCApiClient :
type CCApiClient struct {
	SearchHostTimeObserver            *monitor.TimeObserver
	SearchBizInstTopoTimeObserver     *monitor.TimeObserver
	SearchBusinessTimeObserver        *monitor.TimeObserver
	SearchServiceInstanceTimeObserver *monitor.TimeObserver
	SearchHostCounter                 *monitor.CounterMixin
	SearchBizInstTopoCounter          *monitor.CounterMixin
	SearchBusinessCounter             *monitor.CounterMixin
	SearchServiceInstanceCounter      *monitor.CounterMixin
	GetBizLocationCounter             *monitor.CounterMixin
	client                            *Client
}

// NewCCApiClient :
func NewCCApiClient(client *Client) *CCApiClient {
	return &CCApiClient{
		client: client,
		SearchHostTimeObserver: monitor.NewTimeObserver(MonitorRequestHandledDuration.With(prometheus.Labels{
			"name": "list_biz_host_topo",
		})),
		SearchBizInstTopoTimeObserver: monitor.NewTimeObserver(MonitorRequestHandledDuration.With(prometheus.Labels{
			"name": "search_biz_inst_topo",
		})),
		SearchBusinessTimeObserver: monitor.NewTimeObserver(MonitorRequestHandledDuration.With(prometheus.Labels{
			"name": "search_business",
		})),
		SearchServiceInstanceTimeObserver: monitor.NewTimeObserver(MonitorRequestHandledDuration.With(prometheus.Labels{
			"name": "service_instance",
		})),
		SearchHostCounter: monitor.NewCounterMixin(
			MonitorRequestSuccess.With(prometheus.Labels{
				"name": "list_biz_host_topo",
			}),
			MonitorRequestFails.With(prometheus.Labels{
				"name": "list_biz_host_topo",
			}),
		),
		SearchBizInstTopoCounter: monitor.NewCounterMixin(
			MonitorRequestSuccess.With(prometheus.Labels{
				"name": "search_biz_inst_topo",
			}),
			MonitorRequestFails.With(prometheus.Labels{
				"name": "search_biz_inst_topo",
			}),
		),
		SearchBusinessCounter: monitor.NewCounterMixin(
			MonitorRequestSuccess.With(prometheus.Labels{
				"name": "search_business",
			}),
			MonitorRequestFails.With(prometheus.Labels{
				"name": "search_business",
			}),
		),
		SearchServiceInstanceCounter: monitor.NewCounterMixin(
			MonitorRequestSuccess.With(prometheus.Labels{
				"name": "service_instance",
			}),
			MonitorRequestFails.With(prometheus.Labels{
				"name": "service_instance",
			}),
		),
		GetBizLocationCounter: monitor.NewCounterMixin(
			MonitorRequestSuccess.With(prometheus.Labels{
				"name": "get_biz_location",
			}),
			MonitorRequestFails.With(prometheus.Labels{
				"name": "get_biz_location",
			}),
		),
	}
}

// useApiGateway: use api gateway or not
func (c *CCApiClient) useApiGateway() bool {
	return c.client.conf.GetBool(ConfESBUseAPIGateway)
}

// Agent :
func (c *CCApiClient) Agent() *sling.Sling {
	agent := c.client.Agent()

	// use esb or api gateway
	if c.useApiGateway() {
		customCmdbApi := c.client.conf.GetString(ConfESBCmdbApiAddress)
		if customCmdbApi != "" {
			// use custom cmdb apigw address
			agent = agent.Base(customCmdbApi)
		} else {
			// use default cmdb apigw address
			agent = agent.Path("/api/bk-cmdb/prod")
		}
	} else {
		// use esb cmdb address
		agent = agent.Path("/api/c/compapi/v2/cc")
	}
	return agent
}

func (c *CCApiClient) GetHostsByRange(bkTenantID string, bizID, limit, start int) (*CCSearchHostResponseData, error) {
	defer c.SearchHostTimeObserver.Start().Finish()
	// 返回结果的临时结构定义及声明
	result := struct {
		APIResponse
		Data *CCSearchHostResponseData `json:"data"`
	}{}

	reqBody := &json.Provider{
		Payload: &CCSearchHostRequest{
			Page: CCSearchHostRequestPageInfo{
				Start: start,
				Limit: limit,
				Sort:  "bk_host_id",
			},
			BkBizID: bizID,
			Fields: []string{
				"bk_cloud_id",
				"bk_host_innerip",
				"bk_host_outerip",
				"bk_host_id",
				"dbm_meta",
				"devx_meta",
				"perforce_meta",
				"bk_agent_id",
			},
		},
	}

	// use different path by esb or api gateway
	var path string
	if c.useApiGateway() {
		path = fmt.Sprintf("api/v3/hosts/app/%d/list_hosts_topo", bizID)
	} else {
		path = "list_biz_hosts_topo/"
	}

	response, err := c.Agent().
		Set(authKey, c.client.commonArgs.JSON()).
		Set(tenantKey, bkTenantID).
		Post(path).
		BodyProvider(reqBody).Receive(&result /* success */, &result /* failed */)
	if err != nil {
		c.SearchHostCounter.CounterFails.Inc()
		logging.Errorf("get hosts by range %d:%d failed: %v, %v", start, limit, result, err)
		return nil, err
	}

	logging.Debugf("biz->[%d] get hosts by range start->[%d] limit->[%d] response: %d, %v", bizID, start, limit, response.StatusCode, result.Message)
	if result.Data == nil {
		c.SearchHostCounter.CounterFails.Inc()
		logging.Errorf("%s query from cc error %d: %v", result.RequestID, result.Code, result.Message)
		return nil, errors.Wrapf(define.ErrOperationForbidden, result.Message)
	}

	c.SearchHostCounter.CounterSuccesses.Inc()
	return result.Data, nil
}

// GetSearchBizInstTopo :
func (c *CCApiClient) GetSearchBizInstTopo(bkTenantID string, start, bizID, limit, level int) ([]CCSearchBizInstTopoResponseInfo, error) {
	defer c.SearchBizInstTopoTimeObserver.Start().Finish()
	result := struct {
		APIResponse
		Data []CCSearchBizInstTopoResponseInfo `json:"data"`
	}{}

	sling := c.Agent().Set(authKey, c.client.commonArgs.JSON()).Set(tenantKey, bkTenantID)

	// use different path by esb or api gateway
	var path string
	if c.useApiGateway() {
		path = fmt.Sprintf("api/v3/find/topoinst/biz/%d", bizID)
		sling = sling.Post(path)
	} else {
		path = "search_biz_inst_topo/"
		sling = sling.Get(path)
	}

	response, err := sling.
		QueryStruct(
			&CCSearchBizInstTopoParams{
				BkBizID: bizID,
				Level:   level,
				Start:   start,
				Limit:   limit,
			}).
		Receive(&result, &result)
	if err != nil {
		c.SearchBizInstTopoCounter.CounterFails.Inc()
		logging.Errorf("search biz:%d inst topo %d:%d failed: %v, %v", bizID, start, limit, result, err)
		return nil, err
	}
	logging.Debugf("get biz:%d inst topo by range %d:%d response: %d, %v", bizID, start, limit, response.StatusCode, result.Message)
	if result.Data == nil {
		c.SearchBizInstTopoCounter.CounterFails.Inc()
		logging.Errorf("%s query from cc error %d: %v", result.RequestID, result.Code, result.Message)
		return nil, errors.Wrapf(define.ErrOperationForbidden, result.Message)
	}
	c.SearchBizInstTopoCounter.CounterSuccesses.Inc()
	return result.Data, nil
}

// GetTenantList: 获取租户列表
func (c *CCApiClient) GetTenantList() ([]TenantResponseInfo, error) {
	// 如果未开启多租户模式，则只返回system租户
	if !c.client.conf.GetBool(ConfESBMultiTenantMode) {
		return []TenantResponseInfo{
			{
				ID:     "system",
				Name:   "System",
				Status: "enabled",
			},
		}, nil
	}

	agent := c.Agent().Base(c.client.conf.GetString(ConfESBAddress))
	userApiAddress := c.client.conf.GetString(ConfESBUserApiAddress)
	if userApiAddress == "" {
		agent = agent.Path("/api/bk-user/prod")
	} else {
		agent = agent.Base(userApiAddress)
	}

	result := struct {
		Data []TenantResponseInfo `json:"data"`
	}{}

	response, err := agent.
		Get("api/v3/open/tenants/").
		Set(authKey, c.client.commonArgs.JSON()).
		Set(tenantKey, "system").
		Receive(&result, &result)

	if err != nil {
		logging.Errorf("get tenant list failed: %v", err)
		return nil, errors.Wrapf(err, "get tenant list failed")
	}

	logging.Debugf("get tenant list response: %d", response.StatusCode)

	return result.Data, nil
}

// GetSearchBusiness: 返回全业务,不使用page（目前接口无默认上限值限定，后续CMDB改造后，需要适配兼容）
func (c *CCApiClient) GetSearchBusiness() ([]CCSearchBusinessResponseInfo, error) {
	defer c.SearchBusinessTimeObserver.Start().Finish()

	tenantList, err := c.GetTenantList()
	if err != nil {
		logging.Errorf("get tenant list failed: %v", err)
		return nil, errors.Wrapf(err, "get tenant list failed")
	}

	businessList := make([]CCSearchBusinessResponseInfo, 0)
	for _, tenant := range tenantList {
		// 返回结果结构体声明并创建临时变量
		result := struct {
			APIResponse
			Data *CCSearchBusinessResponseData `json:"data"`
		}{}

		// use different path by esb or api gateway
		var path string
		if c.useApiGateway() {
			path = fmt.Sprintf("api/v3/biz/search/%s", c.client.commonArgs.BkSupplierAccount)
		} else {
			path = "search_business/"
		}

		// 请求并将结果写入到result中
		response, err := c.Agent().
			Post(path).
			Set(authKey, c.client.commonArgs.JSON()).
			Set(tenantKey, tenant.ID).
			BodyProvider(&json.Provider{Payload: &CCSearchBusinessRequest{Fields: []string{"bk_biz_id", "bk_biz_name"}}}).
			Receive(&result /* success */, &result /* failed */)
		if err != nil {
			c.SearchBusinessCounter.CounterFails.Inc()
			logging.Errorf("get business failed: %v, %v", result, err)
			return nil, err
		}

		logging.Debugf("get business response: %d, %v", response.StatusCode, result.Message)
		if result.Data == nil {
			c.SearchBusinessCounter.CounterFails.Inc()
			logging.Errorf("%s query from cc error %d: %v", result.RequestID, result.Code, result.Message)
			return nil, errors.Wrapf(define.ErrOperationForbidden, result.Message)
		}

		// fill bk_tenant_id
		for i := range result.Data.Info {
			result.Data.Info[i].BkTenantID = tenant.ID
		}

		c.SearchBusinessCounter.CounterSuccesses.Inc()
		businessList = append(businessList, result.Data.Info...)
	}

	return businessList, nil
}

// GetServiceInstance : 实例
func (c *CCApiClient) GetServiceInstance(bkTenantID string, bizID, limit, start int, ServiceInstanceIds []int) (*CCSearchServiceInstanceResponseData, error) {
	defer c.SearchServiceInstanceTimeObserver.Start().Finish()
	result := struct {
		APIResponse
		Data *CCSearchServiceInstanceResponseData `json:"data"`
	}{}

	// use different path by esb or api gateway
	var path string
	if c.useApiGateway() {
		path = "api/v3/findmany/proc/service_instance/details"
	} else {
		path = "list_service_instance_detail/"
	}

	response, err := c.Agent().
		Post(path).
		Set(authKey, c.client.commonArgs.JSON()).
		Set(tenantKey, bkTenantID).
		BodyProvider(&json.Provider{Payload: &CCSearchServiceInstanceRequest{
			Page: CCSearchServiceInstanceRequestMetadataLabelPage{
				Start: start,
				Limit: limit,
				Sort:  "bk_host_id",
			},
			BkBizID: bizID,
		}}).
		Receive(&result, &result)
	if err != nil {
		c.SearchServiceInstanceCounter.CounterFails.Inc()
		logging.Errorf("get service_instance failed: %v, %v", result, err)
		return nil, err
	}

	logging.Debugf("get service_instance response: %d, %v", response.StatusCode, result.Message)
	if result.Data == nil {
		c.SearchServiceInstanceCounter.CounterFails.Inc()
		logging.Errorf("%s query from cc error %d: %v", result.RequestID, result.Code, result.Message)
		return nil, errors.Wrapf(define.ErrOperationForbidden, result.Message)
	}

	c.SearchServiceInstanceCounter.CounterSuccesses.Inc()
	return result.Data, nil
}

func (c *CCApiClient) VisitAllHost(ctx context.Context, batchSize int, ccInfo models.CCInfo, fn func(monitor CCSearchHostResponseDataV3Monitor, ccInfo models.CCInfo) error) error {
	taskList, err := GetAllTaskInfo(c, batchSize, ccInfo, fn)
	logging.Debugf("load taskList %v", taskList)
	if err != nil {
		logging.Errorf("get cc cache fail %v", err)
		return err
	}
	taskManager, err := NewTaskManage(ctx, MaxWorkerConfig, func(task Task) {
		var ccHostMonitor *CCSearchHostResponseDataV3Monitor
		ccInfoCopy := ccInfo // 创建一个副本避免捕获循环变量
		switch ccInfoCopy.(type) {
		case *models.CCHostInfo:
			hostRes, err := c.GetHostsByRange(task.BkTenantID, task.BizID, task.Limit, task.Start)
			if err != nil {
				logging.Errorf("unable to load host info to store by %v", err)
				return
			}
			ccHostMonitor, _ = OpenHostResInMonitorAdapter(hostRes, task.BizID)
		case *models.CCInstanceInfo:
			instanceRes, err := c.GetServiceInstance(task.BkTenantID, task.BizID, task.Limit, task.Start, []int{})
			if err != nil {
				logging.Errorf("unable to load instance info to store by %v", err)
				return
			}
			ccHostMonitor, _ = OpenInstanceResInMonitorAdapter(instanceRes, task.BizID)
		}

		for _, topoInfo := range task.Topo {
			MergeTopoHost(ccHostMonitor, TopoDataToCmdbLevelV3(&topoInfo))
		}
		err := fn(*ccHostMonitor, ccInfoCopy)
		if err != nil {
			logging.Errorf("unable to load store by %v", err)
			return
		}
	}, taskList)
	if err != nil {
		logging.Errorf("unable to get all host by %v", err)
		return err
	}
	if taskManager == nil {
		return nil
	}
	err = taskManager.Start()
	if err != nil {
		logging.Errorf("unable to start load model info tasks")
	}
	err = taskManager.Wait()
	if err != nil {
		logging.Errorf("unable to wait load model info tasks")
	}
	err = taskManager.WaitJob()
	if err != nil {
		logging.Errorf("unable to waitJob load model info tasks")
	}
	return taskManager.Stop()
}

// TopoDataToCmdbLevelV3:递归取自定义层级
func TopoDataToCmdbLevelV3(topoData *CCSearchBizInstTopoResponseInfo) []map[string]string {
	var topo []map[string]string
	tempValue := make(map[string]string, 0)
	var getCustomField func(t *CCSearchBizInstTopoResponseInfo, temp map[string]string)

	getCustomField = func(t *CCSearchBizInstTopoResponseInfo, temp map[string]string) {
		if t == nil {
			return
		}
		temp[t.BkObjID] = conv.String(t.Inst)
		if len(t.Child) == 0 {
			topo = append(topo, mapCopyValue(temp))
		}
		for _, value := range t.Child {
			getCustomField(value, temp)
		}
	}
	getCustomField(topoData, tempValue)
	return topo
}

func mapCopy(a, b map[string]string) {
	for key, value := range a {
		b[key] = value
	}
}

func mapCopyValue(a map[string]string) map[string]string {
	b := make(map[string]string, 0)
	mapCopy(a, b)
	return b
}

// 为监控聚合模块 打开主机拓扑结构
func OpenHostResInMonitorAdapter(hostRes *CCSearchHostResponseData, bizID int) (*CCSearchHostResponseDataV3Monitor, int) {
	bkBizID := conv.String(bizID)
	resInfoTopo := make([]CCSearchHostResponseInfoV3Topo, 0)
	for _, ccSearchHostResponseInfoV3 := range hostRes.Info {
		newTopo := make([]map[string]string, 0)
		for _, hostTopoV3 := range ccSearchHostResponseInfoV3.Topo {
			helper := utils.NewMapStringHelper(make(map[string]string))
			helper.Set(define.RecordBkSetID, conv.String(hostTopoV3.BKSetID))
			for _, module := range hostTopoV3.Module {
				helper.Set(define.RecordBkModuleID, conv.String(module.BKModuleID))
				// 接口没有biz 返回，手动补上
				helper.Set(define.RecordBizIDFieldName, bkBizID)
			}
			newTopo = append(newTopo, helper.Data)
		}
		resInfoTopo = append(resInfoTopo, CCSearchHostResponseInfoV3Topo{Host: ccSearchHostResponseInfoV3.Host, BizID: bizID, Topo: newTopo})

	}
	return &CCSearchHostResponseDataV3Monitor{
		Count: hostRes.Count,
		Info:  resInfoTopo,
	}, 0
}

// 为监控聚合模块 打开实例拓扑结构
func OpenInstanceResInMonitorAdapter(instanceRes *CCSearchServiceInstanceResponseData, bizID int) (*CCSearchHostResponseDataV3Monitor, int) {
	bkBizID := conv.String(bizID)
	info := make([]CCSearchHostResponseInfoV3Topo, 0)
	for _, value := range instanceRes.Info {
		hostResponseInfo := CCSearchHostResponseInfoV3Topo{
			Host: CCSearchHostResponseHostInfo{
				BKHostInnerIP: conv.String(value.InstanceID),
			},
		}
		hostResponseInfo.Topo = append(hostResponseInfo.Topo, map[string]string{
			define.RecordBkModuleID: conv.String(value.BKModuleID),
			// 接口没有biz 返回，手动补上
			define.RecordBizIDFieldName: bkBizID,
		})
		hostResponseInfo.BizID = bizID
		info = append(info, hostResponseInfo)
	}
	return &CCSearchHostResponseDataV3Monitor{
		Count: instanceRes.Count,
		Info:  info,
	}, 0
}

// 将自定义topo 放入 Host
func MergeTopoHost(hostInfo *CCSearchHostResponseDataV3Monitor, topoInfo []map[string]string) {
	if hostInfo == nil || topoInfo == nil {
		return
	}
	for hostIndex, host := range hostInfo.Info {
		for moduleIndex, moduleID := range host.Topo {
			for _, topo := range topoInfo {
				topoHelper := utils.NewMapStringHelper(topo)
				moduleHelper := utils.NewMapStringHelper(moduleID)
				if moduleValue, ok := moduleHelper.Get(define.RecordBkModuleID); ok {
					if topoModuleValue, ok := topoHelper.Get(define.RecordBkModuleID); ok {
						if moduleValue == topoModuleValue {
							for key, value := range topo {
								if !moduleHelper.Exists(key) {
									moduleHelper.Set(key, value)
								}
							}
						}
					}
					hostInfo.Info[hostIndex].Topo[moduleIndex] = moduleHelper.Data
				}
			}
		}
	}
}

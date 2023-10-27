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

type APIClient interface {
	GetSearchBusiness() ([]CCSearchBusinessResponseInfo, error)
	GetServiceInstance(bizID, limit, start int, ServiceInstanceIds []int) (*CCSearchServiceInstanceResponseData, error)
	GetSearchBizInstTopo(start, bizID, limit, level int) ([]CCSearchBizInstTopoResponseInfo, error)
	GetHostsByRange(bizID, limit, start int) (*CCSearchHostResponseData, error)
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

// Agent :
func (c *CCApiClient) Agent() *sling.Sling {
	return c.client.Agent().Path("cc/")
}

func (c *CCApiClient) GetHostsByRange(bizID, limit, start int) (*CCSearchHostResponseData, error) {
	defer c.SearchHostTimeObserver.Start().Finish()
	// 返回结果的临时结构定义及声明
	result := struct {
		APIResponse
		Data *CCSearchHostResponseData `json:"data"`
	}{}

	reqBody := &json.Provider{
		Payload: &CCSearchHostRequest{
			CommonArgs: c.client.CommonArgs(),
			Page: CCSearchHostRequestPageInfo{
				Start: start,
				Limit: limit,
				Sort:  "bk_host_id",
			},
			BkBizID: bizID,
			Fields:  []string{"bk_cloud_id", "bk_host_innerip", "bk_host_outerip", "bk_host_id", "dbm_meta", "devx_meta"},
		},
	}
	response, err := c.Agent().Post("list_biz_hosts_topo").BodyProvider(reqBody).Receive(&result /* success */, &result /* failed */)
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
func (c *CCApiClient) GetSearchBizInstTopo(start, bizID, limit, level int) ([]CCSearchBizInstTopoResponseInfo, error) {
	defer c.SearchBizInstTopoTimeObserver.Start().Finish()
	result := struct {
		APIResponse
		Data []CCSearchBizInstTopoResponseInfo `json:"data"`
	}{}
	response, err := c.Agent().Get("search_biz_inst_topo/").
		QueryStruct(
			&CCSearchBizInstTopoParams{
				AppCode:   c.client.commonArgs.AppCode,
				AppSecret: c.client.commonArgs.AppSecret,
				BKToken:   c.client.commonArgs.BKToken,
				UserName:  c.client.commonArgs.UserName,
				BkBizID:   bizID,
				Level:     level,
				Start:     start,
				Limit:     limit,
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

// GetSearchBusiness: 返回全业务,不使用page（目前接口无默认上限值限定，后续CMDB改造后，需要适配兼容）
func (c *CCApiClient) GetSearchBusiness() ([]CCSearchBusinessResponseInfo, error) {
	defer c.SearchBusinessTimeObserver.Start().Finish()
	// 返回结果结构体声明并创建临时变量
	result := struct {
		APIResponse
		Data *CCSearchBusinessResponseData `json:"data"`
	}{}
	// 请求并将结果写入到result中
	response, err := c.Agent().Post("search_business/").BodyProvider(&json.Provider{Payload: &CCSearchBusinessRequest{
		CommonArgs: c.client.CommonArgs(),
		Fields:     []string{"bk_biz_id", "bk_biz_name"},
	}}).Receive(&result /* success */, &result /* failed */)
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

	c.SearchBusinessCounter.CounterSuccesses.Inc()

	// 判断是否需要进行CMDB v3的业务过滤，如果不需要，直接返回
	if !IsFilterCMDBV3Biz {
		logging.Infof("IsFilterCMDBV3Biz is set to->[%t], no biz will filter.", IsFilterCMDBV3Biz)
		return result.Data.Info, nil
	}

	logging.Infof("IsFilterCMDBV3Biz is set to->[%t] will filter biz location.", IsFilterCMDBV3Biz)
	filterResult, err := c.FilterCMDBV3Biz(result.Data.Info)
	logging.Debugf("IsFilterCMDBV3Biz is set to->[%t] will after filter biz count->[%d].", IsFilterCMDBV3Biz, len(filterResult))

	if err != nil {
		logging.Warnf("filter CMDBV3 with error->[%s] will use original data.", err)
		return result.Data.Info, nil
	}

	return filterResult, nil
}

// GetServiceInstance : 实例
func (c *CCApiClient) GetServiceInstance(bizID, limit, start int, ServiceInstanceIds []int) (*CCSearchServiceInstanceResponseData, error) {
	defer c.SearchServiceInstanceTimeObserver.Start().Finish()
	result := struct {
		APIResponse
		Data *CCSearchServiceInstanceResponseData `json:"data"`
	}{}
	response, err := c.Agent().Post("list_service_instance_detail/").BodyProvider(&json.Provider{Payload: &CCSearchServiceInstanceRequest{
		CommonArgs: c.client.CommonArgs(),
		Page: CCSearchServiceInstanceRequestMetadataLabelPage{
			Start: start,
			Limit: limit,
			Sort:  "bk_host_id",
		},
		BkBizID: bizID,
	}}).Receive(&result, &result)
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
		switch ccInfo.(type) {
		case *models.CCHostInfo:
			hostRes, err := c.GetHostsByRange(task.BizID, task.Limit, task.Start)
			if err != nil {
				logging.Errorf("unable to load host info to store by %v", err)
				return
			}
			ccHostMonitor, _ = OpenHostResInMonitorAdapter(hostRes, task.BizID)
		case *models.CCInstanceInfo:
			instanceRes, err := c.GetServiceInstance(task.BizID, task.Limit, task.Start, []int{})
			if err != nil {
				logging.Errorf("unable to load instance info to store by %v", err)
				return
			}
			ccHostMonitor, _ = OpenInstanceResInMonitorAdapter(instanceRes, task.BizID)
		}

		for _, topoInfo := range task.Topo {
			MergeTopoHost(ccHostMonitor, TopoDataToCmdbLevelV3(&topoInfo))
		}
		err := fn(*ccHostMonitor, ccInfo)
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

// FilterBizLocation: 将传入的业务信息进行过滤，仅剩余CMDBV3的业务信息
func (c *CCApiClient) FilterCMDBV3Biz(originalResponse []CCSearchBusinessResponseInfo) ([]CCSearchBusinessResponseInfo, error) {
	var (
		result       CCGetBusinessLocationResponse           // 缓存
		bizList      = make([]int, 0, len(originalResponse)) // 请求业务列表
		V3BizMap     = make(map[int]bool)
		filterResult = make([]CCSearchBusinessResponseInfo, 0)
	)

	// 构造需要请求的业务列表
	for _, bizInfo := range originalResponse {
		bizList = append(bizList, bizInfo.BKBizID)
	}
	logging.Infof("going to request cmdb filter bizList->[%v]", bizList)

	response, err := c.Agent().Post("get_biz_location/").BodyProvider(&json.Provider{Payload: &CCGetBusinessLocationRequest{
		CommonArgs: c.client.CommonArgs(),
		BkBizIDs:   bizList,
	}}).Receive(&result /* success */, &result /* failed */)
	// 判断请求是否成功
	if err != nil {
		c.GetBizLocationCounter.CounterFails.Inc()
		logging.Errorf("get business location failed: %v, %v, cache response will use", result, err)
		return LocationResponseCache, nil
	}

	logging.Debugf("get business location response: %d, %v", response.StatusCode, result.Message)
	if result.Data == nil {
		c.GetBizLocationCounter.CounterFails.Inc()
		logging.Errorf("%s query from cc location error %d: %v, empty response will use", result.RequestID, result.Code, result.Message)
		// 如果返回的内容为空，则此时返回空的业务列表，防止将CMDB拉挂
		return filterResult, errors.Wrapf(define.ErrOperationForbidden, result.Message)
	}

	c.GetBizLocationCounter.CounterSuccesses.Inc()

	// 先过滤一遍所有需要保留的业务信息
	for _, bizInfo := range result.Data {
		if bizInfo.BkLocation != V3LocationLabel {
			logging.Debugf("biz->[%d] location is not v3.0, jump it.", bizInfo.BkBizID)
			continue
		}
		V3BizMap[bizInfo.BkBizID] = true
		logging.Debugf("biz->[%d] location is v3.0, added to map.", bizInfo.BkBizID)
	}

	// 再判断哪些业务需要加入到返回内容中
	for _, originalBizInfo := range originalResponse {
		if _, isV3Biz := V3BizMap[originalBizInfo.BKBizID]; !isV3Biz {
			logging.Infof("biz->[%d] is not v3.0 biz, will not add to return list.", originalBizInfo.BKBizID)
			continue
		}

		logging.Infof("biz->[%d] is V3.0 biz, will added to return list.", originalBizInfo.BKBizID)
		filterResult = append(filterResult, originalBizInfo)
	}

	// 此时更新缓存信息内容，为了下次CMDB被拉挂的时候，可以使用缓存数据
	LocationResponseCache = filterResult
	logging.Infof("location response updated success to count->[%d]", len(LocationResponseCache))

	logging.Infof("filter done, origin biz count->[%d] after filter count->[%d]", len(originalResponse), len(filterResult))
	return filterResult, nil
}

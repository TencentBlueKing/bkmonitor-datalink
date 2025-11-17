// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	apiDefine "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var (
	muForGseApi     sync.Mutex
	muForCmdbApi    sync.RWMutex
	muForBkdataApi  sync.Mutex
	muForMonitorApi sync.RWMutex
)

var (
	gseApi            *bkgse.Client
	cmdbApiClients    map[string]*cmdb.Client
	bkdataApi         *bkdata.Client
	monitorApiClients map[string]*monitor.Client
)

func init() {
	cmdbApiClients = make(map[string]*cmdb.Client)
	monitorApiClients = make(map[string]*monitor.Client)
}

// GetGseApi 获取GseApi客户端
func GetGseApi(bkTenantId string) (*bkgse.Client, error) {
	muForGseApi.Lock()
	defer muForGseApi.Unlock()
	if gseApi != nil {
		return gseApi, nil
	}
	var config define.ClientConfigProvider
	endpoint := cfg.BkApiGseApiGwUrl
	useApiGateWay := true
	if endpoint == "" {
		useApiGateWay = false
		endpoint = fmt.Sprintf("%s/api/c/compapi/v2/gse/", cfg.BkApiUrl)
	}

	adminUser, err := tenant.GetTenantAdminUser(bkTenantId)
	if err != nil {
		return nil, err
	}

	config = bkapi.ClientConfig{
		Endpoint:            endpoint,
		Stage:               cfg.BkApiStage,
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
		AuthorizationParams: map[string]string{"bk_username": adminUser},
	}

	gseApi, err = bkgse.New(useApiGateWay, config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider(), NewHeaderProvider(map[string]string{"X-Bk-Tenant-Id": bkTenantId}))
	if err != nil {
		return nil, err
	}
	return gseApi, nil
}

// GetCmdbApi 获取CmdbApi客户端
func GetCmdbApi(tenantId string) (*cmdb.Client, error) {
	// 首先尝试读锁获取已存在的客户端
	muForCmdbApi.RLock()
	if client, exists := cmdbApiClients[tenantId]; exists {
		muForCmdbApi.RUnlock()
		return client, nil
	}
	muForCmdbApi.RUnlock()

	// 如果不存在，获取写锁创建新客户端
	muForCmdbApi.Lock()
	defer muForCmdbApi.Unlock()

	// 双重检查，防止在等待写锁期间其他goroutine已经创建了客户端
	if client, exists := cmdbApiClients[tenantId]; exists {
		return client, nil
	}

	// 判断是否使用网关
	var endpoint string
	if cfg.BkApiCmdbApiGatewayUrl != "" {
		endpoint = cfg.BkApiCmdbApiGatewayUrl
	} else {
		endpoint = fmt.Sprintf("%s/api/c/compapi/v2/cc/", cfg.BkApiUrl)
	}

	adminUser, err := tenant.GetTenantAdminUser(tenantId)
	if err != nil {
		return nil, err
	}

	config := bkapi.ClientConfig{
		Endpoint:            endpoint,
		AuthorizationParams: map[string]string{"bk_username": adminUser, "bk_supplier_account": "0"},
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
	}

	cmdbApiClients[tenantId], err = cmdb.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider(), NewHeaderProvider(map[string]string{"X-Bk-Tenant-Id": tenantId}))
	if err != nil {
		return nil, err
	}
	return cmdbApiClients[tenantId], nil
}

// GetBkdataApi BkdataApi
func GetBkdataApi(tenantId string) (*bkdata.Client, error) {
	muForBkdataApi.Lock()
	defer muForBkdataApi.Unlock()
	if bkdataApi != nil {
		return bkdataApi, nil
	}
	endpoint := cfg.BkApiBkdataApiBaseUrl
	if endpoint == "" {
		endpoint = fmt.Sprintf("%s/api/c/compapi/data/", cfg.BkApiUrl)
	}

	adminUser, err := tenant.GetTenantAdminUser(tenantId)
	if err != nil {
		return nil, err
	}

	config := bkapi.ClientConfig{
		Endpoint:            endpoint,
		AuthorizationParams: map[string]string{"bk_username": adminUser, "bk_supplier_account": "0"},
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
	}

	bkdataApi, err = bkdata.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider(), NewHeaderProvider(map[string]string{"X-Bk-Tenant-Id": tenantId}))
	if err != nil {
		return nil, err
	}
	return bkdataApi, nil
}

// GetMonitorApi 获取metadataApi客户端
func GetMonitorApi(tenantId string) (*monitor.Client, error) {
	// 首先尝试读锁获取已存在的客户端
	muForMonitorApi.RLock()
	if client, exists := monitorApiClients[tenantId]; exists {
		muForMonitorApi.RUnlock()
		return client, nil
	}
	muForMonitorApi.RUnlock()

	// 如果不存在，获取写锁创建新客户端
	muForMonitorApi.Lock()
	defer muForMonitorApi.Unlock()

	// 双重检查，防止在等待写锁期间其他goroutine已经创建了客户端
	if client, exists := monitorApiClients[tenantId]; exists {
		return client, nil
	}

	endpoint := cfg.BkMonitorApiGatewayBaseUrl
	if endpoint == "" {
		endpoint = fmt.Sprintf("%s/api/c/compapi/v2/monitor_v3/", cfg.BkApiUrl)
	}

	adminUser, err := tenant.GetTenantAdminUser(tenantId)
	if err != nil {
		return nil, err
	}

	config := bkapi.ClientConfig{
		Endpoint:            endpoint,
		Stage:               cfg.BkApiStage,
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
		AuthorizationParams: map[string]string{"bk_username": adminUser},
	}

	monitorApiClients[tenantId], err = monitor.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider(), NewHeaderProvider(map[string]string{"X-Bk-Tenant-Id": tenantId}))
	if err != nil {
		return nil, err
	}
	return monitorApiClients[tenantId], nil
}

// HeaderProvider provide request header.
type HeaderProvider struct {
	Header map[string]string
}

// NewHeaderProvider creates a new HeaderProvider.
func NewHeaderProvider(header map[string]string) *HeaderProvider {
	return &HeaderProvider{
		Header: header,
	}
}

// ApplyToClient will add to the operation operations.
func (p *HeaderProvider) ApplyToClient(cli define.BkApiClient) error {
	return cli.AddOperationOptions(p)
}

// ApplyToOperation will set the body provider.
func (p *HeaderProvider) ApplyToOperation(op define.Operation) error {
	op.SetHeaders(p.Header)
	return nil
}

// HandleApiResultError handle api response error
func HandleApiResultError(result apiDefine.ApiCommonRespMeta, err error, message string) error {
	// handle api request error
	if err != nil {
		return errors.Wrap(err, message)
	}

	// handle api result error
	if err := result.Err(); err != nil {
		return errors.Wrap(err, message)
	}

	return nil
}

// BatchApiRequest send one request first and get the total count, then send the rest requests by pageSize
func BatchApiRequest(pageSize int, getTotalFunc func(any) (int, error), getReqFunc func(page int) define.Operation, concurrency int) ([]any, error) {
	// send the first request to get the total count
	var resp any
	req := getReqFunc(0)
	_, err := req.SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrap(err, "failed to send the first request")
	}

	// 获取总数
	total, err := getTotalFunc(resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the total count")
	}
	// 如果总数为0，直接返回
	if total == 0 {
		return nil, nil
	}

	// 限制并发数
	limitChan := make(chan struct{}, concurrency)
	waitGroup := sync.WaitGroup{}

	// 页数计算，向上取整
	pageCount := (total + pageSize - 1) / pageSize

	// 初始化结果和错误数组
	results := make([]any, pageCount)
	errs := make([]error, pageCount)
	results[0] = resp

	for p := 1; p < pageCount; p++ {
		limitChan <- struct{}{}
		waitGroup.Add(1)
		go func(page int) {
			defer func() {
				<-limitChan
				waitGroup.Done()
			}()
			var r any
			req := getReqFunc(page)
			_, err := req.SetResult(&r).Request()
			if err != nil {
				errs[page] = errors.Wrap(err, fmt.Sprintf("failed to send the request for page %d", page))
				errs = append(errs, errors.Wrap(err, fmt.Sprintf("failed to send the request for page %d", page)))
				return
			}
			results[page] = r
		}(p)
	}

	waitGroup.Wait()

	// 检查是否有错误
	for _, err := range errs {
		if err != nil {
			return nil, errors.New("failed to send the rest requests")
		}
	}

	return results, nil
}

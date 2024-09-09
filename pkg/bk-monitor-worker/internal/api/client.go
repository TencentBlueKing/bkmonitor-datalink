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
	"strings"
	"sync"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsclustermanager"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsproject"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsstorage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	apiDefine "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var (
	muForGseApi            sync.Mutex
	muForBcsApi            sync.Mutex
	muForBcsProjectApi     sync.Mutex
	muForBcsClusterManager sync.Mutex
	muForBcsStorage        sync.Mutex
	muForCmdbApi           sync.Mutex
	muForNodemanApi        sync.Mutex
	muForBkdataApi         sync.Mutex
	muForMetadataApi       sync.Mutex
	muForMonitorApi        sync.Mutex
)

var (
	gseApi            *bkgse.Client
	bcsApi            *bcs.Client
	bcsProjectApi     *bcsproject.Client
	bcsClusterManager *bcsclustermanager.Client
	bcsStorage        *bcsstorage.Client
	cmdbApi           *cmdb.Client
	nodemanApi        *nodeman.Client
	bkdataApi         *bkdata.Client
	metadataApi       *metadata.Client
	monitorApi        *monitor.Client
)

// GetGseApi 获取GseApi客户端
func GetGseApi() (*bkgse.Client, error) {
	muForGseApi.Lock()
	defer muForGseApi.Unlock()
	if gseApi != nil {
		return gseApi, nil
	}
	var config define.ClientConfigProvider
	useApiGateWay := cfg.BkApiEnabled
	if useApiGateWay {
		config = bkapi.ClientConfig{
			Endpoint:      cfg.BkApiGseApiGwUrl,
			Stage:         cfg.BkApiStage,
			AppCode:       cfg.BkApiAppCode,
			AppSecret:     cfg.BkApiAppSecret,
			JsonMarshaler: jsonx.Marshal,
		}
	} else {
		config = bkapi.ClientConfig{
			Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/gse/", cfg.BkApiUrl),
			Stage:               cfg.BkApiStage,
			AppCode:             cfg.BkApiAppCode,
			AppSecret:           cfg.BkApiAppSecret,
			JsonMarshaler:       jsonx.Marshal,
			AuthorizationParams: map[string]string{"bk_username": "admin"},
		}
	}
	var err error
	gseApi, err = bkgse.New(useApiGateWay, config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return gseApi, nil
}

// GetBcsApi 获取BcsApi客户端
func GetBcsApi() (*bcs.Client, error) {
	muForBcsApi.Lock()
	defer muForBcsApi.Unlock()
	if bcsApi != nil {
		return bcsApi, nil
	}
	config := bkapi.ClientConfig{
		Endpoint: strings.TrimRight(cfg.BkApiBcsApiGatewayBaseUrl, "/"),
		AuthorizationParams: map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", cfg.BkApiBcsApiGatewayToken),
		},
		AppCode:       cfg.BkApiAppCode,
		AppSecret:     cfg.BkApiAppSecret,
		JsonMarshaler: jsonx.Marshal,
	}
	var err error
	bcsApi, err = bcs.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return bcsApi, nil
}

// GetBcsStorageApi 获取BcsStorageApi客户端
func GetBcsStorageApi() (*bcsstorage.Client, error) {
	muForBcsStorage.Lock()
	defer muForBcsStorage.Unlock()
	if bcsClusterManager != nil {
		return bcsStorage, nil
	}
	config := bkapi.ClientConfig{
		Endpoint:      fmt.Sprintf("%s/bcsapi/v4/storage/k8s/dynamic/all_resources/clusters", strings.TrimRight(cfg.BkApiBcsApiMicroGwUrl, "/")),
		JsonMarshaler: jsonx.Marshal,
	}
	var err error
	bcsStorage, err = bcsstorage.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider(), NewHeaderProvider(map[string]string{"Authorization": fmt.Sprintf("Bearer %s", cfg.BkApiBcsApiGatewayToken)}))
	if err != nil {
		return nil, err
	}
	return bcsStorage, nil
}

// GetBcsClusterManagerApi 获取BcsClusterManagerApi客户端
func GetBcsClusterManagerApi() (*bcsclustermanager.Client, error) {
	muForBcsClusterManager.Lock()
	defer muForBcsClusterManager.Unlock()
	if bcsClusterManager != nil {
		return bcsClusterManager, nil
	}
	config := bkapi.ClientConfig{
		Endpoint:      fmt.Sprintf("%s/bcsapi/v4/clustermanager/v1/", strings.TrimRight(cfg.BkApiBcsApiMicroGwUrl, "/")),
		JsonMarshaler: jsonx.Marshal,
	}
	var err error
	bcsClusterManager, err = bcsclustermanager.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider(), NewHeaderProvider(map[string]string{"Authorization": fmt.Sprintf("Bearer %s", cfg.BkApiBcsApiGatewayToken)}))
	if err != nil {
		return nil, err
	}
	return bcsClusterManager, nil
}

// GetBcsProjectApi 获取GetBcsProjectApi客户端
func GetBcsProjectApi() (*bcsproject.Client, error) {
	muForBcsProjectApi.Lock()
	defer muForBcsProjectApi.Unlock()
	if bcsProjectApi != nil {
		return bcsProjectApi, nil
	}
	config := bkapi.ClientConfig{
		Endpoint:      fmt.Sprintf("%s/bcsapi/v4/bcsproject/v1/", strings.TrimRight(cfg.BkApiBcsApiMicroGwUrl, "/")),
		JsonMarshaler: jsonx.Marshal,
	}
	var err error
	bcsProjectApi, err = bcsproject.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider(), NewHeaderProvider(map[string]string{"Authorization": fmt.Sprintf("Bearer %s", cfg.BkApiBcsApiGatewayToken), "X-Project-Username": "admin"}))
	if err != nil {
		return nil, err
	}
	return bcsProjectApi, nil
}

// GetCmdbApi 获取CmdbApi客户端
func GetCmdbApi() (*cmdb.Client, error) {
	muForCmdbApi.Lock()
	defer muForCmdbApi.Unlock()
	if cmdbApi != nil {
		return cmdbApi, nil
	}
	config := bkapi.ClientConfig{
		Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/cc/", cfg.BkApiUrl),
		AuthorizationParams: map[string]string{"bk_username": "admin", "bk_supplier_account": "0"},
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
	}

	var err error
	cmdbApi, err = cmdb.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return cmdbApi, nil
}

// GetNodemanApi NodemanApi
func GetNodemanApi() (*nodeman.Client, error) {
	muForNodemanApi.Lock()
	defer muForNodemanApi.Unlock()
	if nodemanApi != nil {
		return nodemanApi, nil
	}
	endpoint := cfg.BkApiNodemanApiBaseUrl
	if endpoint == "" {
		endpoint = fmt.Sprintf("%s/api/c/compapi/v2/nodeman/", cfg.BkApiUrl)
	}
	config := bkapi.ClientConfig{
		Endpoint:            endpoint,
		AuthorizationParams: map[string]string{"bk_username": "admin", "bk_supplier_account": "0"},
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
	}

	var err error
	nodemanApi, err = nodeman.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return nodemanApi, nil
}

// GetBkdataApi BkdataApi
func GetBkdataApi() (*bkdata.Client, error) {
	muForBkdataApi.Lock()
	defer muForBkdataApi.Unlock()
	if bkdataApi != nil {
		return bkdataApi, nil
	}
	endpoint := cfg.BkApiBkdataApiBaseUrl
	if endpoint == "" {
		endpoint = fmt.Sprintf("%s/api/c/compapi/data/", cfg.BkApiUrl)
	}
	config := bkapi.ClientConfig{
		Endpoint:            endpoint,
		AuthorizationParams: map[string]string{"bk_username": "admin", "bk_supplier_account": "0"},
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
	}

	var err error
	bkdataApi, err = bkdata.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return bkdataApi, nil
}

// GetMetadataApi 获取metadataApi客户端
func GetMetadataApi() (*metadata.Client, error) {
	muForMetadataApi.Lock()
	defer muForMetadataApi.Unlock()
	if metadataApi != nil {
		return metadataApi, nil
	}
	config := bkapi.ClientConfig{
		Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/monitor_v3/", cfg.BkApiUrl),
		AuthorizationParams: map[string]string{"bk_username": "admin", "bk_supplier_account": "0"},
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
	}

	var err error
	metadataApi, err = metadata.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return metadataApi, nil
}

// GetMonitorApi 获取metadataApi客户端
func GetMonitorApi() (*monitor.Client, error) {
	muForMonitorApi.Lock()
	defer muForMonitorApi.Unlock()
	if monitorApi != nil {
		return monitorApi, nil
	}
	var config define.ClientConfigProvider
	useBkMonitorApigw := cfg.BkMonitorApiGatewayEnabled
	if useBkMonitorApigw {
		config = bkapi.ClientConfig{
			Endpoint:      cfg.BkMonitorApiGatewayBaseUrl,
			Stage:         cfg.BkMonitorApiGatewayStage,
			AppCode:       cfg.BkApiAppCode,
			AppSecret:     cfg.BkApiAppSecret,
			JsonMarshaler: jsonx.Marshal,
		}
	} else {
		config = bkapi.ClientConfig{
			Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/monitor_v3/", cfg.BkApiUrl),
			Stage:               cfg.BkApiStage,
			AppCode:             cfg.BkApiAppCode,
			AppSecret:           cfg.BkApiAppSecret,
			JsonMarshaler:       jsonx.Marshal,
			AuthorizationParams: map[string]string{"bk_username": "admin"},
		}
	}
	var err error
	monitorApi, err = monitor.New(useBkMonitorApigw, config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return monitorApi, nil
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
func BatchApiRequest(pageSize int, getTotalFunc func(interface{}) (int, error), getReqFunc func(page int) define.Operation, concurrency int) ([]interface{}, error) {
	// send the first request to get the total count
	var resp interface{}
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
	results := make([]interface{}, pageCount)
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
			var r interface{}
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

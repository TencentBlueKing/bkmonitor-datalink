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

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsclustermanager"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsproject"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var (
	muForGseApi            sync.Mutex
	muForBcsApi            sync.Mutex
	muForBcsProjectApi     sync.Mutex
	muForBcsClusterManager sync.Mutex
	muForCmdbApi           sync.Mutex
	muForNodemanApi        sync.Mutex
	muForBkdataApi         sync.Mutex
)

var (
	gseApi            *bkgse.Client
	bcsApi            *bcs.Client
	bcsProjectApi     *bcsproject.Client
	bcsClusterManager *bcsclustermanager.Client
	cmdbApi           *cmdb.Client
	nodemanApi        *nodeman.Client
	bkdataApi         *bkdata.Client
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
			BkApiUrlTmpl:  fmt.Sprintf("%s/api/{api_name}/", cfg.BkApiUrl),
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
		Endpoint:      fmt.Sprintf("%s/bcsapi/v4/clustermanager/v1/", strings.TrimRight(cfg.BkApiBcsApiMicroGwUrl, "/")),
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
	if bcsClusterManager != nil {
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

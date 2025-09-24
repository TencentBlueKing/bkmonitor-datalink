// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apiservice

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var Gse GseService

type GseService struct{}

// QueryRoute 查询路由配置
func (GseService) QueryRoute(bkTenantId string, params bkgse.QueryRouteParams) (any, error) {
	gseApi, err := api.GetGseApi(bkTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "get gse api failed")
	}
	if params.Condition.ChannelId == 0 {
		return nil, errors.Errorf("condition.channel_id can not be empty")
	}
	if params.Operation.OperatorName == "" {
		params.Operation.OperatorName = "admin"
	}
	var resp define.APICommonResp
	_, err = gseApi.QueryRoute().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "QueryRoute with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "QueryRoute with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// AddRoute 注册路由配置
func (GseService) AddRoute(bkTenantId string, params bkgse.AddRouteParams) (any, error) {
	gseApi, err := api.GetGseApi(bkTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "get gse api failed")
	}
	if params.Operation.OperatorName == "" {
		params.Operation.OperatorName = "admin"
	}
	var resp define.APICommonResp
	_, err = gseApi.AddRoute().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "AddRoute with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "AddRoute with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

// UpdateRoute 更新路由配置
func (GseService) UpdateRoute(bkTenantId string, params bkgse.UpdateRouteParams) (any, error) {
	gseApi, err := api.GetGseApi(bkTenantId)
	if err != nil {
		return nil, errors.Wrap(err, "get gse api failed")
	}
	if params.Specification == nil {
		return nil, errors.Errorf("specification can not be empty")
	}
	if params.Operation.OperatorName == "" {
		params.Operation.OperatorName = "admin"
	}
	var resp define.APICommonResp
	_, err = gseApi.UpdateRoute().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "UpdateRoute with params [%s] failed", paramStr)
	}
	if err := resp.Err(); err != nil {
		paramStr, _ := jsonx.MarshalString(params)
		return nil, errors.Wrapf(err, "UpdateRoute with params [%s] failed", paramStr)
	}
	return resp.Data, nil
}

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
	"strconv"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/metadata"
)

var Metadata MetadataService

type MetadataService struct{}

// CustomTimeSeriesDetail 获取自定义ts信息
func (MetadataService) CustomTimeSeriesDetail(bkBizId int, timeSeriesGroupId uint, modelOnly bool) (*metadata.CustomTimeSeriesDetailData, error) {
	params := map[string]string{
		"bk_biz_id":            strconv.Itoa(bkBizId),
		"time_series_group_id": strconv.Itoa(int(timeSeriesGroupId)),
		"model_only":           strconv.FormatBool(modelOnly),
	}
	api, err := api.GetMetadataApi()
	if err != nil {
		return nil, errors.Wrap(err, "get metadata api failed")
	}
	var result metadata.CustomTimeSeriesDetailResp
	_, err = api.CustomTimeSeriesDetail().SetQueryParams(params).SetResult(&result).Request()
	if err != nil {
		return nil, errors.Wrapf(err, "CustomTimeSeriesDetail with bk_biz_id [%v] time_series_group_id [%v] modelOnly [%v] failed", bkBizId, timeSeriesGroupId, modelOnly)
	}
	if !result.Result {
		return nil, nil
	}
	return &result.Data, nil

}

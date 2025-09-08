// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"github.com/pkg/errors"
)

type ApiCommonRespMeta struct {
	Message string `json:"message" mapstructure:"message"`
	Result  bool   `json:"result" mapstructure:"result"`
	Code    int    `json:"code" mapstructure:"code"`
}

func (m ApiCommonRespMeta) Err() error {
	if !m.Result {
		return errors.Errorf("api result is false, message [%s]", m.Message)
	}
	return nil
}

// APICommonResp api通用返回结构体
type APICommonResp struct {
	ApiCommonRespMeta
	Data any `json:"data"`
}

// APICommonMapResp api通用返回结构体Map
type APICommonMapResp struct {
	ApiCommonRespMeta
	Data map[string]any `json:"data"`
}

// APICommonListResp api通用返回结构体List
type APICommonListResp struct {
	ApiCommonRespMeta
	Data []any `json:"data"`
}

// APICommonListMapResp api通用返回结构体List-Map
type APICommonListMapResp struct {
	ApiCommonRespMeta
	Data []map[string]any `json:"data"`
}

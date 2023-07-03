// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	DefaultTargetHost     string = "127.0.0.1"
	DefaultTargetPort     int    = 80
	DefaultResponseFormat string = "startswith"
)

// SimpleMatchParam :
type SimpleMatchParam struct {
	Request       string `config:"request"`
	RequestFormat string `config:"request_format" validate:"regexp=^(raw|hex)?$"`
	Response      string `config:"response"`
	// 支持匹配函数：reg, eq, nq, startswith, nstartswith, endswith, nendswith, in, nin, wildcard
	// 支持数据格式：raw, hex
	// ResponseFormat: $format|$function 比如 raw|ng / raw|in / hex|nin
	ResponseFormat string `config:"response_format"`
}

// CleanParams :
func (c *SimpleMatchParam) CleanParams() error {
	var err error

	if c.RequestFormat == "" {
		c.RequestFormat = utils.ConvTypeRaw
	}
	if c.RequestFormat == utils.ConvTypeHex {
		if c.Request != "" {
			_, err = utils.ConvertHexStringToBytes(c.Request)
			if err != nil {
				logger.Errorf("ConvertHexStringToBytes error:%v", err)
				return define.ErrTypeConvertError
			}
		}
	}

	if strings.HasPrefix(c.ResponseFormat, utils.ConvTypeHex) {
		if c.Response != "" {
			_, err = utils.ConvertHexStringToBytes(c.Response)
			if err != nil {
				logger.Errorf("ConvertHexStringToBytes error:%v", err)
				return define.ErrTypeConvertError
			}
		}
	}

	hexPrefix := utils.ConvTypeHex + "|"
	if strings.HasPrefix(c.ResponseFormat, hexPrefix) {
		c.ResponseFormat = c.ResponseFormat[len(hexPrefix):]
	}

	rawPrefix := utils.ConvTypeRaw + "|"
	if strings.HasPrefix(c.ResponseFormat, rawPrefix) {
		c.ResponseFormat = c.ResponseFormat[len(rawPrefix):]
	}

	if c.ResponseFormat == "" {
		c.ResponseFormat = DefaultResponseFormat
	} else if c.ResponseFormat == utils.MatchWildcard {
		c.Response = utils.WildcardToRegex(c.Response)
	}

	if err != nil {
		return err
	}
	return nil
}

// SimpleTaskParam :
type SimpleTaskParam struct {
	TargetHost string `config:"target_host"`
	// 支持多个目标，当配置多个目标时忽略单个目标配置
	TargetHostList []string `config:"target_host_list"`
	TargetPort     int      `config:"target_port" validate:"required,min=1"`
}

// CleanParams :
func (c *SimpleTaskParam) CleanParams() error {
	if len(c.TargetHostList) > 0 {
		// 当配置多个目标时忽略单个目标配置
		c.TargetHost = ""
	}

	return nil
}

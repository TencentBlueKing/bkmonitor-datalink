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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

const (
	PaginationTypeLimitOffset = "limit_offset"
	PaginationTypePageNumber  = "page_number"
)

const (
	HttpBodyTypeFormData   = "form_data"
	HttpBodyTypeUrlEncoded = "x_www_form_urlencoded"
)

const (
	HttpAuthTypeBasic        = "basic_auth"
	HttpAuthTypeBearerToken  = "bearer_token"
	HttpAuthTypeTencentCloud = "tencent_auth"
)

const (
	HttpBodyContentTypeText = "text"
	HttpBodyContentTypeHtml = "html"
	HttpBodyContentTypeXml  = "xml"
	HttpBodyContentTypeJson = "json"
)

type HttpKVPair struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Desc      string `json:"desc"`
	IsEnabled bool   `json:"is_enabled"`
}

type HttpBodyConfig struct {
	DataType    string       `json:"data_type"`
	Params      []HttpKVPair `json:"params"`
	Content     string       `json:"content"`
	ContentType string       `json:"content_type"`
}

type TencentApiAuth struct {
	SecretId  string `json:"secret_id"`
	SecretKey string `json:"secret_key"`
	Version   string `json:"version"`
	Action    string `json:"action"`
	Region    string `json:"region"`
}

type AuthOption struct {
	UserName       string         `json:"username"`
	Password       string         `json:"password"`
	Token          string         `json:"token"`
	TencentApiAuth TencentApiAuth `json:"tencent_api_auth"`
}

type HttpAuthorizeConfig struct {
	// 鉴权相关
	Type   string     `json:"auth_type"`
	Option AuthOption `json:"auth_config"`
}

type HttpPaginationConfig struct {
	// 分页相关
	Type   string `json:"type"`
	Option struct {
		// 最大页码
		MaxSize int `json:"max_size"`
		// 每一页的数量
		PageSize int `json:"page_size"`
		// 获取事件总数的参数路径
		TotalPath string `json:"total_path"`
	} `json:"option"`
}

type HttpPullPlugin struct {
	Plugin

	// 事件解析相关
	SourceFormat   string `json:"source_format"`
	MultipleEvents bool   `json:"multiple_events"`
	EventsPath     string `json:"events_path"`

	// 请求内容相关
	URL        string               `json:"url"`
	Method     string               `json:"method"`
	Headers    []HttpKVPair         `json:"headers"`
	Params     []HttpKVPair         `json:"query_params"`
	Body       HttpBodyConfig       `json:"body"`
	Authorize  HttpAuthorizeConfig  `json:"authorize"`
	Pagination HttpPaginationConfig `json:"pagination"`

	// 请求调度相关
	Interval   int    `json:"interval"`
	Overlap    int    `json:"overlap"`
	Timeout    int    `json:"timeout"`
	TimeFormat string `json:"time_format"`
}

func NewHttpPullPlugin(cfg interface{}) (*HttpPullPlugin, error) {
	h := &HttpPullPlugin{}

	h.MultipleEvents = false
	h.EventsPath = ""

	h.Interval = 60
	h.Overlap = 10
	h.Timeout = 60
	h.TimeFormat = "epoch_second"

	err := utils.ConvertByJSON(cfg, h)
	if err != nil {
		return nil, err
	}
	return h, nil
}

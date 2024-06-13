// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"

// CommonArgs :
type CommonArgs struct {
	AppCode           string `url:"bk_app_code,omitempty" json:"bk_app_code,omitempty"`
	AppSecret         string `url:"bk_app_secret,omitempty" json:"bk_app_secret,omitempty"`
	BKToken           string `url:"bk_token,omitempty" json:"bk_token,omitempty"`
	UserName          string `url:"bk_username,omitempty" json:"bk_username,omitempty"`
	BkSupplierAccount string `url:"bk_supplier_account,omitempty" json:"bk_supplier_account,omitempty"`
}

func (c CommonArgs) JSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// APIResponse :
type APIResponse struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	Result    bool   `json:"result"`
	RequestID string `json:"request_id"`
}

// Copy :
func (c CommonArgs) Copy() CommonArgs {
	return c
}

// ESB :
var (
	ESB                   *Client
	MaxWorkerConfig       int // 同时并发访问ESB的客户端个数
	IsFilterCMDBV3Biz     bool
	LocationResponseCache []CCSearchBusinessResponseInfo // LocationCache缓存，但是此处没有做持久化，考虑是transfer启动的时候如果CMDB也是挂的，服务失效也符合预期
)

const (
	V3LocationLabel = "v3.0"
)

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
	"github.com/dghubble/sling"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// Client :
type Client struct {
	commonArgs *CommonArgs
	agent      *sling.Sling
	conf       define.Configuration
}

// CommonArgs :
func (c *Client) CommonArgs() *CommonArgs {
	if c.commonArgs == nil {
		return nil
	}

	commonArgs := c.commonArgs.Copy()
	return &commonArgs
}

// Agent :
func (c *Client) Agent() *sling.Sling {
	return c.agent.New()
}

// Post :
func (c *Client) Post(path string) *sling.Sling {
	return c.Agent().Post(path)
}

// Get :
func (c *Client) Get(path string) *sling.Sling {
	return c.Agent().Get(path)
}

// NewClient :
func NewClient(conf define.Configuration) *Client {
	return NewClientWithDoer(conf, nil)
}

// NewClientWithDoer :
func NewClientWithDoer(conf define.Configuration, doer sling.Doer) *Client {
	return &Client{
		commonArgs: &CommonArgs{
			AppCode:           conf.GetString(ConfESBAppCodeKey),
			AppSecret:         conf.GetString(ConfESBAppSecretKey),
			UserName:          conf.GetString(ConfESBUserNameKey),
			BkSupplierAccount: conf.GetString(ConfESBBkSupplierAccount),
		},
		agent: sling.New().Base(conf.GetString(ConfESBAddress)).Doer(doer),
		conf:  conf,
	}
}

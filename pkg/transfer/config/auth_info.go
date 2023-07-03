// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"

// AuthInfo :
type SimpleMetaAuthInfo struct {
	*MetaClusterInfo
}

// NewAuthInfo :
func NewAuthInfo(conf *MetaClusterInfo) *SimpleMetaAuthInfo {
	return &SimpleMetaAuthInfo{
		MetaClusterInfo: conf,
	}
}

// GetAuthInfo : 获取认证信息
func (c *SimpleMetaAuthInfo) GetUserName() (string, error) {
	switch val := c.MustGetAuthInfo("username").(type) {
	case string:
		return val, nil
	default:
		return "", define.ErrType

	}
}

// SetAuthInfo : 设置认证信息
func (c *SimpleMetaAuthInfo) SetUserName(val string) {
	c.AuthInfo["username"] = val
}

// GetAuthInfo : 获取认证信息
func (c *SimpleMetaAuthInfo) GetPassword() (string, error) {
	switch val := c.MustGetAuthInfo("password").(type) {
	case string:
		return val, nil
	default:
		return "", define.ErrType
	}
}

// SetAuthInfo : 设置认证信息
func (c *SimpleMetaAuthInfo) SetPassword(val string) {
	c.AuthInfo["password"] = val
}

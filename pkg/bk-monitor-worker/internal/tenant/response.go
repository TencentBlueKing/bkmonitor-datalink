// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2025 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

type ListTenantResp struct {
	Data []ListTenantData `json:"data"`
}

type ListTenantData struct {
	Id     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type BatchLookupVirtualUserResp struct {
	Data []BatchLookupVirtualUserData `json:"data"`
}

type BatchLookupVirtualUserData struct {
	BkUsername  string `json:"bk_username" mapstructure:"bk_username"`
	LoginName   string `json:"login_name" mapstructure:"login_name"`
	DisplayName string `json:"display_name" mapstructure:"display_name"`
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

// ESInfo
type ESInfo struct {
	Host           string
	Username       string
	Password       string
	MaxConcurrency int
}

// TableInfo
type TableInfo struct {
	// 存储id，对应一个es实例(或集群)
	StorageID int
	// 别名生成格式
	AliasFormat string
	// 日期生成格式,会被time.Format使用
	DateFormat string
	// 日期步长,单位: h
	DateStep int
}

// AliasInfo
type AliasInfo struct {
	Aliases map[string]any `json:"aliases"`
}

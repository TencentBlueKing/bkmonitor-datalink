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
	"time"
)

// HeartBeatConfig :  心跳配置，该配置是供bkmonitorbeat及uptimecheckbeat共同使用
type HeartBeatConfig struct {
	// bkmonitorbeeat下的心跳data id配置
	GlobalDataID int32 `config:"global_dataid" `
	ChildDataID  int32 `config:"child_dataid"` // 暂无用到
	// uptimecheckbeat下的心跳data id配置，缺少子任务的data_id
	DataID             int32         `config:"dataid"`
	Period             time.Duration `config:"period" validate:"min=1s"`
	PublishImmediately bool          `config:"publish_immediately"`
}

// NewHeartBeatConfig :
func NewHeartBeatConfig() *HeartBeatConfig {
	config := &HeartBeatConfig{
		GlobalDataID:       0,
		ChildDataID:        0,
		Period:             time.Minute,
		PublishImmediately: true,
	}
	return config
}

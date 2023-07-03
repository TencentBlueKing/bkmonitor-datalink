// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package configs 该文件提供给tcp udp http script作为TaskConfig基类使用，其他的任务的TaskConfig基类已修改为base.go中的BaseTaskConfig
package configs

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

// 默认的网络通信读写缓存长度
const (
	DefaultBufferSize int = 102400
)

type IPType int32

const (
	IPAuto IPType = 0
	IPv4   IPType = 4
	IPv6   IPType = 6
)

type ProtocolType int32

const (
	Http ProtocolType = 0
	Tcp  ProtocolType = 1
	Udp  ProtocolType = 2
	Icmp ProtocolType = 2
)

// CheckMode 域名检测模式
type CheckMode string

const (
	// CheckModeAll 检测域名所有IP
	CheckModeAll CheckMode = "all"
	// CheckModeSingle 检测域名单个IP
	CheckModeSingle CheckMode = "single"
	// DefaultDNSCheckMode 默认域名检测模式
	DefaultDNSCheckMode = CheckModeSingle
)

// Unpack 必须实现该接口方法，否则现有配置解析库解析该类型字段会panic
func (c *CheckMode) Unpack(s string) error {
	*c = CheckMode(s)
	return nil
}

// NetTaskParam : tcp task parameter
type NetTaskParam struct {
	BaseTaskParam `config:"_,inline"`
	BufferSize    int    `config:"buffer_size" validate:"positive"`
	TargetIPType  IPType `config:"target_ip_type"`
	// 域名检测模式
	DNSCheckMode CheckMode `config:"dns_check_mode"`
}

// GetDataID :
func (t *NetTaskParam) GetDataID() int32 {
	return t.DataID
}

// CleanParams :
func (t *NetTaskParam) CleanParams() error {
	err := t.BaseTaskParam.CleanParams()
	if err != nil {
		return err
	}
	if t.BufferSize == 0 {
		t.BufferSize = DefaultBufferSize
	}
	if t.DNSCheckMode == "" {
		// 未配置则使用默认模式
		t.DNSCheckMode = DefaultDNSCheckMode
	}
	return nil
}

// GetLabels :
func (t *NetTaskParam) GetLabels() []map[string]string {
	return t.Labels
	// return make(map[string]string)
}

// GetIdent :
func (t *NetTaskParam) GetIdent() string {
	return t.Ident
}

// GetTimeout :
func (t *NetTaskParam) GetTimeout() time.Duration {
	return t.Timeout
}

// GetAvailableDuration :
func (t *NetTaskParam) GetAvailableDuration() time.Duration {
	return t.AvailableDuration
}

// GetPeriod :
func (t *NetTaskParam) GetPeriod() time.Duration {
	return t.Period
}

// GetBizID :
func (t *NetTaskParam) GetBizID() int32 {
	return t.BizID
}

// GetTaskID :
func (t *NetTaskParam) GetTaskID() int32 {
	return t.TaskID
}

// NetTaskMetaParam : task parameter
type NetTaskMetaParam struct {
	BaseTaskMetaParam `config:"_,inline"`
	MaxBufferSize     int    `config:"max_buffer_size" validate:"positive"`
	TaskConfigPath    string `config:"config_path"`
}

// CleanParams :
func (c *NetTaskMetaParam) CleanParams() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskMetaParam)
	if err != nil {
		return err
	}
	if c.MaxBufferSize == 0 {
		c.MaxBufferSize = DefaultBufferSize
	}
	return nil
}

// CleanTask 覆盖了BaseTaskMetaParam的CleanTask逻辑，因为增加了buffersize
func (c *NetTaskMetaParam) CleanTask(task define.TaskConfig) error {
	err := c.BaseTaskMetaParam.CleanTask(task)
	if err != nil {
		return err
	}
	bt, ok := utils.GetPtrByName(task, "NetTaskParam")
	if !ok {
		panic(define.ErrType)
	}
	baseTask := bt.(*NetTaskParam)

	if c.MaxBufferSize < baseTask.BufferSize || baseTask.BufferSize == 0 {
		baseTask.BufferSize = c.MaxBufferSize
	}
	err = task.InitIdent()
	if err != nil {
		return err
	}
	return nil
}

// NewNetTaskMetaParam :
func NewNetTaskMetaParam() NetTaskMetaParam {
	return NetTaskMetaParam{
		BaseTaskMetaParam: BaseTaskMetaParam{
			DataID:     0,
			MaxTimeout: define.DefaultTimeout,
			MinPeriod:  define.DefaultPeriod,
		},
		MaxBufferSize: DefaultBufferSize,
	}
}

// GetDataID :
func (c *NetTaskMetaParam) GetDataID() int32 {
	return c.DataID
}

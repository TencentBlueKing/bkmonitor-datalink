// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package keyword

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	DimensionMissingErr = errors.New("failed to get dimension")
)

const (
	KeySeparator = "||" // 生成的key连接符
)

// KeywordTaskResult  日志关键字匹配后的任务结果结构体，用与processor和sender之间的数据交互
type KeywordTaskResult struct {
	FilePath     string            // 文件路径
	RuleName     string            // 规则名
	SortedFields []string          // 正则中待提取的字段名
	Dimensions   map[string]string // 维度信息
	Log          string            // 日志内容
}

// MakeKey 根据一个结果，构建出可以统计的key, 出于降低大字符串在程序中流转的考虑，此处返回的是sha1结果
// key的构建：文件路径 + 规则名 + 维度（维度名=维度值|维度名=维度值）
func (k *KeywordTaskResult) MakeKey() (string, error) {
	var (
		key            bytes.Buffer
		dimensionValue string
		ok             bool
		hash           = sha1.New()
	)
	// 写入基本的文件名及规则名
	key.WriteString(k.FilePath)
	key.WriteString(KeySeparator)
	key.WriteString(k.RuleName)
	key.WriteString(KeySeparator)

	// 逐一判断和获取正则中的分组信息，如果获取失败，表明这个信息是100%有问题的
	for _, fieldName := range k.SortedFields {
		key.WriteString(fieldName)
		key.WriteString("=")

		if dimensionValue, ok = k.Dimensions[fieldName]; !ok {
			logger.Errorf("failed to get field->[%s] value", fieldName)
			return "", errors.Wrapf(DimensionMissingErr, fieldName)
		}
		key.WriteString(dimensionValue)
		key.WriteString(KeySeparator)
	}

	// 返回sha1的结果
	hash.Write(key.Bytes())
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

type TaskConfig struct {
	TaskID   string
	TaskType string

	RawText configs.KeywordTaskConfig

	Input     InputConfig
	Processer ProcessConfig
	Sender    SendConfig
	IPLinker  chan interface{}
	PSLinker  chan interface{}

	Ctx       context.Context
	CtxCancel context.CancelFunc
}

func (tc *TaskConfig) Same(obj *TaskConfig) bool {
	b1, err := json.Marshal(tc.RawText)
	if err != nil {
		return false
	}
	b2, err := json.Marshal(obj.RawText)
	if err != nil {
		return false
	}
	if string(b1) != string(b2) {
		return false
	}
	return true
}

type InputConfig struct {
	DataID        int
	Paths         []string // 采集路径
	ScanFrequency time.Duration
	CloseInactive time.Duration
	ExcludeFiles  []*regexp.Regexp
}

type SendConfig struct {
	// 公共配置
	DataID int // data_id

	// 日志采集配置
	CanPackage   bool
	PackageCount int
	ExtMeta      interface{}
	GroupInfo    interface{}

	// 日志关键字配置
	Target       string          // 监控目标
	ReportPeriod time.Duration   // 上报周期
	OutputFormat string          // 上报方式
	TimeUnit     string          // 上报时间单位
	Label        []configs.Label // 配置下发模块信息
}

type ProcessConfig struct {
	// 公共配置
	DataID    int           // data_id
	Encoding  string        // 文件编码
	ScanSleep time.Duration // 文件扫描休眠时间

	// 过滤规则配置
	HasFilter      bool     // 是否过滤
	FilterPatterns []string // 过滤规则

	// 日志关键字配置
	KeywordConfigs []configs.KeywordConfig // 日志关键字配置信息
}

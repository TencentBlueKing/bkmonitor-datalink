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
	"fmt"
	"path/filepath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var rl = newRecordLogs(LogConfig{Stdout: true})

func SetLogConfig(c LogConfig) {
	c.Level = "info" // MUST BE 'INFO'
	rl = newRecordLogs(c)
}

type recordLogs struct {
	l logger.Logger
}

func newRecordLogs(c LogConfig) *recordLogs {
	l := logger.New(logger.Options{
		Stdout:        c.Stdout,
		Filename:      filepath.Join(c.Path, "bkmonitorbeat.record"),
		MaxSize:       c.MaxSize,
		MaxAge:        c.MaxAge,
		MaxBackups:    c.Backups,
		Level:         c.Level,
		DisableCaller: true,
	})
	return &recordLogs{l: l}
}

func RecordLog(template string, kvs []LogKV) {
	template = fmt.Sprintf("%s; fields=%+v", template, kvs)
	rl.l.Info(template)
}

type LogKV struct {
	K string
	V interface{}
}

func (kv LogKV) String() string {
	return fmt.Sprintf("%s=(%v)", kv.K, kv.V)
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sidecar

import (
	"bytes"
	"os"
	"text/template"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const privilegedFile = "privileged.conf"

const templatePrivileged = `
type: "privileged"
processor:
  - name: "token_checker/fixed"
    config:
      type: "fixed"
      fixed_token: {{ .Token }}
      traces_dataid: {{ .TracesDataID }}
      metrics_dataid: {{ .MetricsDataID }}
      logs_dataid: {{ .LogsDataID }}
      profiles_dataid: {{ .ProfilesDataID }}
      biz_id: {{ .BizID }}
      app_name: {{ .AppName }}
`

type privilegedConfig struct {
	Token          string
	TracesDataID   int
	MetricsDataID  int
	LogsDataID     int
	ProfilesDataID int
	BizID          int32
	AppName        string
}

func (s *Sidecar) handlePrivilegedConfigFile() error {
	path := s.realPath(privilegedFile)
	currConfig := s.getPrivilegedConfig()

	// 如果 dataid 被删除则且配置文件存在需要把配置文件删除
	var empty privilegedConfig
	if currConfig == empty {
		if utils.PathExist(path) {
			logger.Infof("remove privileged config file (%s)", path)
			return os.Remove(path)
		}
		return nil
	}

	// 生成新配置文件内容
	tmpl, err := template.New("Privileged").Parse(templatePrivileged)
	if err != nil {
		return err
	}
	buf := &bytes.Buffer{}
	if err := tmpl.Execute(buf, currConfig); err != nil {
		return err
	}

	// 判断是否需要更新配置文件
	b, _ := os.ReadFile(path)
	if bytes.Equal(b, buf.Bytes()) {
		return nil
	}

	logger.Infof("create or update privileged config file (%s)", path)

	err = os.WriteFile(path, buf.Bytes(), 0o666)
	if err != nil {
		return err
	}

	// 最后一步通知 collector reload
	return s.sendReloadSignal()
}

func (s *Sidecar) getPrivilegedConfig() privilegedConfig {
	ids := s.dw.DataIDs()

	var config privilegedConfig
	for _, id := range ids {
		if id.Token != "" {
			config.Token = id.Token
		}

		if id.BizID != 0 {
			config.BizID = id.BizID
		}

		if id.AppName != "" {
			config.AppName = id.AppName
		}
		switch id.Type {
		case define.RecordTraces.S():
			config.TracesDataID = id.DataID
		case define.RecordMetrics.S():
			config.MetricsDataID = id.DataID
		case define.RecordLogs.S():
			config.LogsDataID = id.DataID
		case define.RecordProfiles.S():
			config.ProfilesDataID = id.DataID
		}
	}
	return config
}

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
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/pkg/errors"
	"k8s.io/client-go/rest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Config struct {
	ConfigPath    string    `yaml:"config_path"`
	PidPath       string    `yaml:"pid_path"`
	Kubconfig     string    `yaml:"kubeconfig"`
	ApiServerHost string    `yaml:"apiserver_host"`
	Tls           TlsConfig `yaml:"tls"`
}

type TlsConfig struct {
	Insecure bool   `yaml:"insecure"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

type Sidecar struct {
	ctx     context.Context
	cancel  context.CancelFunc
	watcher *Watcher
	config  *Config
}

func New(ctx context.Context, config *Config) (*Sidecar, error) {
	if config == nil {
		return nil, errors.New("nil config")
	}

	if err := os.Setenv("KUBECONFIG", config.Kubconfig); err != nil {
		return nil, err
	}

	tlsConf := &rest.TLSClientConfig{
		Insecure: config.Tls.Insecure,
		CertFile: config.Tls.CertFile,
		KeyFile:  config.Tls.KeyFile,
		CAFile:   config.Tls.CAFile,
	}
	client, err := k8sutils.NewBKClient(config.ApiServerHost, tlsConf)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	return &Sidecar{
		ctx:     ctx,
		cancel:  cancel,
		watcher: newWatcher(ctx, client),
		config:  config,
	}, nil
}

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

func (s *Sidecar) Start() error {
	if err := s.watcher.Start(); err != nil {
		return err
	}

	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return nil

		case <-ticker.C:
			err := s.updatePrivilegedConfigFile()
			if err != nil {
				logger.Errorf("failed to update privileged config: %v", err)
			}
		}
	}
}

func (s *Sidecar) Stop() {
	s.cancel()
}

func (s *Sidecar) getPrivilegedConfig() privilegedConfig {
	ids := s.watcher.DataIDs()

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

func (s *Sidecar) updatePrivilegedConfigFile() error {
	path := filepath.Join(s.config.ConfigPath, privilegedFile)
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
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = f.Write(buf.Bytes())
	if err != nil {
		return err
	}

	// 最后一步通知 collector reload
	return s.sendReloadSignal()
}

func (s *Sidecar) sendReloadSignal() error {
	content, err := os.ReadFile(s.config.PidPath)
	if err != nil {
		return err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return errors.Wrap(err, "convert pid failed")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return errors.Wrap(err, "find process failed")
	}

	if err = process.Signal(syscall.SIGUSR1); err != nil {
		return errors.Wrap(err, "publish signal failed")
	}
	logger.Infof("reload finished, pid=%d", pid)
	return nil
}

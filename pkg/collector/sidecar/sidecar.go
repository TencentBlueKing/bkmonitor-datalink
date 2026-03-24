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
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"k8s.io/client-go/rest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Sidecar struct {
	ctx    context.Context
	cancel context.CancelFunc

	config *Config
	sm     *secretManager
}

func New(ctx context.Context, config *Config) (*Sidecar, error) {
	if config == nil {
		return nil, errors.New("nil config")
	}
	logger.Infof("sidecar configs: %+v", config)

	if err := os.Setenv("KUBECONFIG", config.KubConfig); err != nil {
		return nil, err
	}

	tlsConf := &rest.TLSClientConfig{
		Insecure: config.TLS.Insecure,
		CertFile: config.TLS.CertFile,
		KeyFile:  config.TLS.KeyFile,
		CAFile:   config.TLS.CAFile,
	}

	client, err := k8sutils.NewK8SClient(config.ApiServerHost, tlsConf)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	return &Sidecar{
		ctx:    ctx,
		cancel: cancel,
		sm:     newSecretManager(ctx, config.Secret, client),
		config: config,
	}, nil
}

func (s *Sidecar) Start() error {
	if err := s.sm.Start(); err != nil {
		return err
	}

	smTimer := time.NewTimer(time.Hour)
	smTimer.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return nil

		case cf := <-s.sm.Watch():
			err := s.handleChildConfigFiles(cf)
			if err != nil {
				logger.Errorf("%s child config (%s) failed: %v", cf.action, cf.name, err)
			}
			smTimer.Reset(time.Second * 5)

		case <-smTimer.C: // 信号收敛
			if err := s.sendReloadSignal(); err != nil {
				logger.Errorf("child config changed, but reload failed: %v", err)
			}
		}
	}
}

func (s *Sidecar) Stop() {
	s.cancel()
	s.sm.Stop()
}

func (s *Sidecar) realPath(p string) string {
	return filepath.Join(s.config.ConfigPath, p)
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
		return errors.Wrap(err, "send reload signal failed")
	}
	logger.Infof("reload finished, pid=%d", pid)
	return nil
}

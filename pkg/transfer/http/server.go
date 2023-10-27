// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"net"
	"net/http"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// Server :
type Server struct {
	*define.BaseTask
	*http.Server
	conf define.Configuration
}

// Start :
func (s *Server) Start() error {
	err := s.BaseTask.Start()
	if err != nil {
		return err
	}

	return s.BaseTask.Activate(func(ctx context.Context) {
		e := s.ListenAndServe()
		switch e {
		case http.ErrServerClosed:
			logging.Infof("http server closed")
		case nil:
			logging.Infof("http server listen on %s", s.Addr)
		default:
			logging.Fatalf("http server listen error %v", e)
		}
	})
}

// Stop :
func (s *Server) Stop() error {
	err := s.BaseTask.Activate(func(ctx context.Context) {
		if !s.conf.GetBool(ConfAutoShutdown) {
			return
		}
		err := s.Shutdown(ctx)
		switch err {
		case context.Canceled:
			logging.Infof("http server context canceled")
		case nil:
			break
		default:
			logging.Infof("http server stop error %v", err)
		}
	})
	if err != nil {
		return err
	}
	return s.BaseTask.Stop()
}

// NewServer :
func NewServer(ctx context.Context, conf define.Configuration) define.Task {
	return &Server{
		conf:     conf,
		BaseTask: define.NewBaseTask(ctx),
		Server: &http.Server{
			Addr: net.JoinHostPort(conf.GetString(define.ConfHost), strconv.Itoa(conf.GetInt(define.ConfPort))),
			Handler: &AuthHandler{
				Handler:      http.DefaultServeMux,
				Token:        conf.GetString(ConfAuthToken),
				PublicPrefix: conf.GetStringSlice(ConfAuthExemptPath),
			},
		},
	}
}

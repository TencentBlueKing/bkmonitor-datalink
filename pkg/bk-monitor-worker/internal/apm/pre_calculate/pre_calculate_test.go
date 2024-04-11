// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const dataIdFilePath = "./connections_test.yaml"

func TestApmPreCalculateViaFile(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	op, err := Initial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	errChan := make(chan error, 1)
	go op.Run(errChan)
	runErr := <-errChan
	if runErr != nil {
		logger.Fatal("failed to run")
	}
	close(errChan)
	go op.WatchConnections(dataIdFilePath)

	s := make(chan os.Signal)
	signal.Notify(s, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		switch <-s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			cancel()
			logger.Infof("Bye")
			os.Exit(0)
		}
	}
}

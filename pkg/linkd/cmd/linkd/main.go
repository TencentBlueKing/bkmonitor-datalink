// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Command linkd 是 Linkd 服务的进程入口。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"linkd/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 所有常驻角色都使用正式默认进程装配，并各自初始化 telemetry；嵌入和测试可通过 Dependencies 替换。
	command := cli.NewRootCommand(version, cli.Dependencies{})
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(command.ErrOrStderr(), err)
		os.Exit(1)
	}
}

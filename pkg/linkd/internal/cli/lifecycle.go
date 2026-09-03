// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"github.com/spf13/cobra"
	"linkd/internal/telemetry"
)

func newLifecycleCommand(options *commandOptions) *cobra.Command {
	return newProcessCommand(options, processCommand{
		use:      "lifecycle",
		short:    "启动 Alert 生命周期处理进程",
		role:     telemetry.RoleLifecycle,
		validate: options.lifecycleValidate,
		run:      options.lifecycleRunner,
	})
}

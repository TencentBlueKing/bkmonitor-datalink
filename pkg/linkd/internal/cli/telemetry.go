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
	"context"
	"errors"
	"fmt"
	"time"

	"linkd/internal/config"
	"linkd/internal/telemetry"
)

const telemetryShutdownTimeout = 5 * time.Second

func runWithTelemetry(
	ctx context.Context,
	cfg config.Config,
	role telemetry.Role,
	version string,
	run func(context.Context, *telemetry.Runtime) error,
) (runErr error) {
	if run == nil {
		return fmt.Errorf("run with telemetry: runner must not be nil")
	}
	telemetryConfig := telemetry.Config{}
	if cfg.Telemetry != nil {
		telemetryConfig = *cfg.Telemetry
	}
	runtime, err := telemetry.Start(ctx, telemetryConfig, role, version)
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), telemetryShutdownTimeout)
		defer cancel()
		if err := runtime.Shutdown(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	return run(ctx, runtime)
}

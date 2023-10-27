// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log

import (
	"context"

	"github.com/spf13/viper"
	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/otelLog"
)

type Logger interface {
	OtelLogger() *otelzap.Logger
	ZapLogger() *zap.Logger
	Debugf(ctx context.Context, format string, v ...any)
	Infof(ctx context.Context, format string, v ...any)
	Warnf(ctx context.Context, format string, v ...any)
	Errorf(ctx context.Context, format string, v ...any)
	Panicf(ctx context.Context, format string, v ...any)
	Fatalf(ctx context.Context, format string, v ...any)
}

func setDefault() {
	viper.SetDefault(PathConfigPath, "")
	viper.SetDefault(LevelConfigPath, "info")
}

func NewLogger() Logger {
	setDefault()

	path := viper.GetString(PathConfigPath)
	level := viper.GetString(LevelConfigPath)

	return otelLog.NewLogger(&otelLog.OtelOption{
		Level: level,
		Path:  path,
	})
}

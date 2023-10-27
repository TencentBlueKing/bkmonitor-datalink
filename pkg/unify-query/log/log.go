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
	"fmt"
)

func Warnf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Warn(fmt.Sprintf(format, v...))
}

func Infof(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Info(fmt.Sprintf(format, v...))
}

func Errorf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Error(fmt.Sprintf(format, v...))
}

func Debugf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Debug(fmt.Sprintf(format, v...))
}

func Panicf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Panic(fmt.Sprintf(format, v...))
}

func Fatalf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Fatal(fmt.Sprintf(format, v...))
}

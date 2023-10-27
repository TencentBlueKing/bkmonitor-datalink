// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// Configuration :
var Configuration define.Configuration

// NewConfiguration : create a Configuration
var NewConfiguration func() define.Configuration

// FromContext : get configuration from context
func FromContext(ctx context.Context) define.Configuration {
	conf := ctx.Value(define.ContextConfigKey)
	if conf == nil {
		return nil
	}
	return conf.(define.Configuration)
}

// IntoContext : put configuration into context
func IntoContext(ctx context.Context, conf define.Configuration) context.Context {
	return context.WithValue(ctx, define.ContextConfigKey, conf)
}

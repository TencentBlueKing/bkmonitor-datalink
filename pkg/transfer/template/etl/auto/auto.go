// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package auto

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
)

// NewSchema
func NewSchema(ctx context.Context) (etl.Schema, error) {
	rt := config.ResultTableConfigFromContext(ctx)
	builder := etl.NewContainerSchemaBuilder()
	err := builder.Apply(
		// 前两个Plugin为builder添加PrepareRecord
		// 当PrepareRecord的Transform方法执行时，是对from数据进行修改，不会将数据清洗至to
		PrepareByResultTablePlugin(rt),
		etl.StandardBeatFieldsPlugin,
		// 添加SimpleRecord
		etl.DefaultTimeAliasFieldPlugin(define.TimeStampFieldName),
		SchemaByResultTablePlugin(rt),
	)
	if err != nil {
		return nil, err
	}
	return builder.Finish(), nil
}

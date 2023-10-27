// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// NewStandardPipeline :
func NewStandardLogPipeline(ctx context.Context, name string, etl string) (*pipeline.Pipeline, error) {
	builder, err := pipeline.NewLogConfigBuilder(ctx, name)
	if err != nil {
		return nil, err
	}
	return builder.BuildStandardBranchingByETLName(etl)
}

// StdLogPipelineCreatorByETLName
func StdLogPipelineCreatorByETLName(etl string) func(ctx context.Context, name string) (define.Pipeline, error) {
	return func(ctx context.Context, name string) (define.Pipeline, error) {
		return NewStandardLogPipeline(ctx, name, etl)
	}
}

const (
	TypeLogText      = "bk_log_text"
	TypeLogJson      = "bk_log_json"
	TypeLogSeparator = "bk_log_separator"
	TypeLogRegexp    = "bk_log_regexp"
)

func init() {
	define.RegisterPipeline(TypeLogText, StdLogPipelineCreatorByETLName("text_log"))
	define.RegisterPipeline(TypeLogJson, StdLogPipelineCreatorByETLName("json_log"))
	define.RegisterPipeline(TypeLogSeparator, StdLogPipelineCreatorByETLName("separator_log"))
	define.RegisterPipeline(TypeLogRegexp, StdLogPipelineCreatorByETLName("regexp_log"))
}

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
func NewStandardV2EventPipeline(ctx context.Context, name string) (define.Pipeline, error) {
	builder, err := pipeline.NewEventConfigBuilder(ctx, name)
	if err != nil {
		return nil, err
	}
	return builder.BuildBranchingWithGluttonous(nil, func(ctx context.Context, from pipeline.Node, to pipeline.Node) error {
		return builder.ConnectStandardNodesByETLName(ctx, "bk_standard_v2_event", from, to)
	})
}

const TypeStandardV2Event = "bk_standard_v2_event"

func init() {
	define.RegisterPipeline(TypeStandardV2Event, NewStandardV2EventPipeline)
}

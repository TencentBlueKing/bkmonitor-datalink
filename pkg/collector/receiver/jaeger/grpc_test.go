// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jaeger

import (
	"context"
	"testing"

	"github.com/jaegertracing/jaeger/proto-gen/api_v2"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
)

func TestGrpcPostSpansPreCheck(t *testing.T) {
	t.Run("Failed", func(t *testing.T) {
		svc := GrpcService{}
		svc.Validator = pipeline.Validator{
			Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, errors.New("MUST ERROR")
			},
		}

		req := &api_v2.PostSpansRequest{}
		_, err := svc.PostSpans(context.Background(), req)
		assert.Error(t, err)
	})

	t.Run("Success", func(t *testing.T) {
		svc := GrpcService{}
		svc.Validator = pipeline.Validator{
			Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			},
		}

		req := &api_v2.PostSpansRequest{}
		_, err := svc.PostSpans(context.Background(), req)
		assert.NoError(t, err)
	})
}

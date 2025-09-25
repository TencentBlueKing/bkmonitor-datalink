// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package errno

import (
	"errors"
	"testing"
)

func TestSpecificLogFormats(t *testing.T) {
	t.Run("双层错误嵌套", func(t *testing.T) {
		inner := ErrBusinessParamInvalid().WithOperation("内层操作")
		outer := ErrBusinessLogicError().WithOperation("外层操作").WithError(inner)
		result := outer.String()
		t.Log("输出:", result)
	})

	t.Run("三层错误嵌套", func(t *testing.T) {
		// 最内层：数据解析错误
		innermost := ErrDataDeserializeFailed().
			WithComponent("JSON Parser").
			WithOperation("解析响应体").
			WithContext("字段", "user_data")

		// 中间层：存储连接错误
		middle := ErrStorageConnFailed().
			WithComponent("Elasticsearch").
			WithOperation("搜索查询").
			WithContext("索引", "metrics-2024").
			WithError(innermost)

		// 最外层：业务逻辑错误
		outer := ErrBusinessQueryExecution().
			WithComponent("QueryService").
			WithOperation("执行查询").
			WithContext("查询ID", "q-12345").
			WithError(middle)

		result := outer.String()
		t.Log("三层错误输出:", result)
	})

	t.Run("四层错误嵌套", func(t *testing.T) {
		// 最底层：普通错误
		baseErr := errors.New("connection refused")

		// 第一层：数据处理错误
		level1 := ErrDataProcessFailed().
			WithOperation("读取数据").
			WithError(baseErr)

		// 第二层：存储连接错误
		level2 := ErrStorageConnFailed().
			WithComponent("InfluxDB").
			WithOperation("查询时序数据").
			WithContext("数据库", "monitor_db").
			WithError(level1)

		// 第三层：查询解析错误
		level3 := ErrQueryParseInvalidSQL().
			WithOperation("执行SQL查询").
			WithContext("SQL", "SELECT * FROM metrics").
			WithError(level2)

		// 最外层：业务错误
		level4 := ErrBusinessQueryExecution().
			WithComponent("APIGateway").
			WithOperation("处理用户请求").
			WithContext("用户ID", "user-789").
			WithError(level3)

		result := level4.String()
		t.Log("四层错误输出:", result)
	})

	t.Run("混合类型错误链", func(t *testing.T) {
		// ErrCode包装普通error
		inner := ErrDataFormatInvalid().
			WithOperation("验证数据格式").
			WithError(errors.New("invalid timestamp format"))

		outer := ErrBusinessLogicError().
			WithComponent("DataValidator").
			WithOperation("数据校验").
			WithContext("数据源", "kafka-topic-1").
			WithError(inner)

		result := outer.String()
		t.Log("混合错误输出:", result)
	})
}

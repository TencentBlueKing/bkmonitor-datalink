// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// FlatBatch splits the configured array and overlays each item onto its envelope.
func FlatBatch(field string, strict bool) (*PayloadDecoder, *FunctionalRecord) {
	decoder := NewPayloadDecoder().FissionSplitHandler(strict, ExtractByJMESPath(field), "", field)
	return decoder, NewFunctionalRecord("", func(from Container, to Container) error {
		for _, key := range from.Keys() {
			v, err := from.Get(key)
			if err != nil {
				return err
			}
			err = to.Put(key, v)
			if err != nil {
				return err
			}
		}

		v, err := from.Get(field)
		if err != nil {
			// 缺少拆分字段时保留外层字段。
			return nil
		}

		var items map[string]interface{}

		switch value := v.(type) {
		case Container:
			items = ContainerToMap(value)
		case map[string]interface{}:
			items = value
		default:
			return errors.Wrapf(define.ErrType, "%T", v)
		}

		for key, value := range items {
			err = to.Put(key, value)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

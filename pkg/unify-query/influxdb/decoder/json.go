// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package decoder

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

var jsonType = "application/json"

// JSONDecoder
type JSONDecoder struct{}

// Decode :
func (d *JSONDecoder) Decode(ctx context.Context, reader io.Reader, resp *Response) (size int, err error) {
	resp.Ctx = ctx
	resp.Results = make([]Result, 0)

	rd := bufio.NewReader(reader)
	for {
		select {
		case <-ctx.Done():
			err = fmt.Errorf("context timeout")
			return size, err
		default:
			var line []byte
			line, err = rd.ReadBytes('\n')
			if len(line) > 0 {
				res := new(Response)
				size += len(line)
				err = json.Unmarshal(line, res)
				if err != nil {
					return size, err
				}
				resp.Results = append(resp.Results, res.Results...)
			}

			if err == io.EOF {
				err = nil
				return size, err
			}
			if err != nil {
				return size, err
			}
		}
	}
}

// init
func init() {
	decoders[jsonType] = new(JSONDecoder)
}

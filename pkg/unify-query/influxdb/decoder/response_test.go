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
	"context"
	"fmt"
	"os"
	"testing"
)

// TestMSGPack
func TestMSGPack(t *testing.T) {
	ctx := context.Background()
	data, err := os.Open("testfile/msgData")
	if err != nil {
		panic(err)
	}
	dec, _ := GetDecoder("application/x-msgpack")
	resp := new(Response)
	size, err := dec.Decode(ctx, data, resp)
	if err != nil {
		panic(err)
	}
	if resp != nil {
		fmt.Println(size)
		fmt.Println(resp)
	}
}

// TestJSON
func TestJSON(t *testing.T) {
	ctx := context.Background()
	data, err := os.Open("testfile/jsonData.json")
	if err != nil {
		panic(err)
	}
	dec, _ := GetDecoder("application/json")
	resp := new(Response)
	size, err := dec.Decode(ctx, data, resp)
	if err != nil {
		panic(err)
	}
	if resp != nil {
		fmt.Println(size)
		fmt.Println(resp)
	}
}

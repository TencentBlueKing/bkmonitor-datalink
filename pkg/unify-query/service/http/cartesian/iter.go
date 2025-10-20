// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cartesian

import (
	"sync"
)

// Iter takes interface-slices and returns a channel, receiving cartesian products
func Iter(params ...[]any) chan []any {
	// create channel
	c := make(chan []any)
	// create waitgroup
	var wg sync.WaitGroup
	// call iterator
	wg.Add(1)
	iterate(&wg, c, []any{}, params...)
	// call channel-closing go-func
	go func() { wg.Wait(); close(c) }()
	// return channel
	return c
}

// private, recursive Iteration-Function
func iterate(wg *sync.WaitGroup, channel chan []any, result []any, params ...[]any) {
	// dec WaitGroup when finished
	defer wg.Done()
	// no more params left?
	if len(params) == 0 {
		// send result to channel
		channel <- result
		return
	}
	// shift first param
	p, params := params[0], params[1:]
	// iterate over it
	for i := 0; i < len(p); i++ {
		// inc WaitGroup
		wg.Add(1)
		// create copy of result
		resultCopy := append([]any{}, result...)
		// call self with remaining params
		go iterate(wg, channel, append(resultCopy, p[i]), params...)
	}
}

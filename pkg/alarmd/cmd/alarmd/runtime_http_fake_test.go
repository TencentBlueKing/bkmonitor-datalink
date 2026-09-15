// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"net/http"
	"time"
)

// fakeHTTPRuntime lets a runtime test start without binding a port. It lived
// with the phase-one runtime's tests until that runtime was retired; the
// phase-two tests were already using it, so it moved here rather than being
// deleted with its old neighbours.
type fakeHTTPRuntime struct {
	run func(context.Context, string, time.Duration) error
	api http.Handler
}

func (runtime *fakeHTTPRuntime) Run(ctx context.Context, address string, timeout time.Duration) error {
	return runtime.run(ctx, address, timeout)
}

func (runtime *fakeHTTPRuntime) SetAPI(handler http.Handler) { runtime.api = handler }

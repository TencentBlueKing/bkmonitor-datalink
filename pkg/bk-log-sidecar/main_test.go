// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package main

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestServeHTTPProfListenErrorDoesNotPanic(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(io.Discard)))

	assert.NotPanics(t, func() {
		serveHTTPProf("127.0.0.1:16060", func(addr string, handler http.Handler) error {
			assert.Equal(t, "127.0.0.1:16060", addr)
			assert.Nil(t, handler)
			return errors.New("listen failed")
		})
	})
}

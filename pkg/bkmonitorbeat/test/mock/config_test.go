// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"testing"

	"github.com/facebookgo/inject"
)

// TestInjectConfig :
func TestInjectConfig(t *testing.T) {
	var (
		g    inject.Graph
		conf Config
		err  error
	)
	err = g.Provide(
		&inject.Object{Value: &conf},
	)
	if err != nil {
		t.Errorf("provide error: %v", err)
	}

	err = g.Populate()
	if err != nil {
		t.Errorf("populate error: %v", err)
	}
	err = conf.Clean()
	if err != nil {
		t.Errorf(err.Error())
	}
	if conf.Task.Task.GetIdent() != "test" {
		t.Errorf("config clean error")
	}
}

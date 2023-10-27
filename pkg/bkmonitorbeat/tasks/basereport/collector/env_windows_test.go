// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"io"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockCmd struct {
	stdout io.Reader
	err    error
}

func (m *mockCmd) StdoutPipe() (io.ReadCloser, error) {
	return ioutil.NopCloser(m.stdout), m.err
}

func (m *mockCmd) Start() error {
	return m.err
}

func TestGetProcs(t *testing.T) {
	want := 0
	got, err := GetProcs()
	assert.NoError(t, err)
	assert.Equal(t, got, want)
}

func TestGetMaxFiles(t *testing.T) {
	num, err := GetMaxFiles()
	assert.Nil(t, err)
	if num <= 0 {
		t.Errorf("GetMaxFiles error and num is zero.")
	}
}

func TestGetUname(t *testing.T) {
	uname, err := GetUname()
	assert.NoError(t, err)
	assert.NotEmpty(t, uname)
}

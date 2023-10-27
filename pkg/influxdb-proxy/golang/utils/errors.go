// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"bytes"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/errors"
)

// CheckError : check error and panic
func CheckError(err error) {
	if err != nil {
		panic(err)
	}
}

// RecoverError :
func RecoverError(fn func(error)) {
	v := recover()
	switch err := v.(type) {
	case nil:
		return
	case error:
		fn(errors.WithStack(err))
	default:
		panic(v)
	}
}

// MultiErrors :
type MultiErrors struct {
	errors []error
}

// Add :
func (m *MultiErrors) Add(err error) {
	m.errors = append(m.errors, err)
}

// AsError :
func (m *MultiErrors) AsError() error {
	if len(m.errors) == 0 {
		return nil
	}
	return m
}

// Error :
func (m *MultiErrors) Error() string {
	var buffer bytes.Buffer
	for _, e := range m.errors {
		_, err := fmt.Fprintln(&buffer, e.Error())
		CheckError(err)
	}
	return buffer.String()
}

// NewMultiErrors :
func NewMultiErrors() *MultiErrors {
	return &MultiErrors{
		errors: make([]error, 0),
	}
}

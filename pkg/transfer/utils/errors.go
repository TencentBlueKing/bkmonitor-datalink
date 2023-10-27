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
	"runtime/debug"
	"sync"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

var MonitorPanicTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Namespace: define.AppName,
	Name:      "panic_total",
	Help:      "Panic count of program",
})

func init() {
	prometheus.MustRegister(MonitorPanicTotal)
}

type causeError interface {
	error
	Cause() error
}

// CheckError : check error and panic
func CheckError(err error) {
	if err != nil {
		panic(err)
	}
}

// CheckFnError
func CheckFnError(fn func() error) {
	CheckError(fn())
}

// RecoverError :
func RecoverError(fn func(error)) {
	v := recover()
	if v != nil {
		MonitorPanicTotal.Add(1)
		fmt.Println("stacktrace from panic: \n" + string(debug.Stack()))
	}
	switch err := v.(type) {
	case nil:
		return
	case causeError:
		fn(err)
	case error:
		fn(errors.Errorf("%v", err))
	default:
		fn(errors.Errorf("%v", v))
	}
}

// MultiErrorCollector
type MultiErrorCollector struct {
	err *MultiErrors
	ch  chan error
	wg  sync.WaitGroup
}

// Channel
func (c *MultiErrorCollector) Channel() chan<- error {
	return c.ch
}

// Start
func (c *MultiErrorCollector) Start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.err.Collect(c.ch)
	}()
}

// Stop
func (c *MultiErrorCollector) Stop() {
	close(c.ch)
	c.wg.Wait()
}

// NewMultiErrorCollector
func NewMultiErrorCollector(err *MultiErrors) *MultiErrorCollector {
	ch := make(chan error)
	return &MultiErrorCollector{
		err: err,
		ch:  ch,
	}
}

// MultiErrors :
type MultiErrors struct {
	errors []error
}

// Add :
func (m *MultiErrors) Add(err error) {
	if err != nil {
		m.errors = append(m.errors, err)
	}
}

// AsError :
func (m *MultiErrors) AsError() error {
	if len(m.errors) == 0 {
		return nil
	}
	return errors.WithStack(m)
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

// Collect
func (m *MultiErrors) Collect(ch chan error) {
	for err := range ch {
		m.Add(err)
	}
}

// NewMultiErrors :
func NewMultiErrors() *MultiErrors {
	return &MultiErrors{
		errors: make([]error, 0),
	}
}

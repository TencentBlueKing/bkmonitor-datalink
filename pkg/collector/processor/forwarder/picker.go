// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package forwarder

import (
	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash/v2"
	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}

type innerMember string

func (m innerMember) String() string {
	return string(m)
}

type Picker struct {
	c *consistent.Consistent
}

func NewPicker() *Picker {
	cfg := consistent.Config{
		PartitionCount:    8,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            hasher{},
	}
	c := consistent.New(nil, cfg)
	return &Picker{c: c}
}

func (p *Picker) AddMember(s string) {
	p.c.Add(innerMember(s))
}

func (p *Picker) RemoveMember(s string) {
	p.c.Remove(s)
}

func (p *Picker) PickTraces(rs ptrace.Traces) (string, error) {
	b, err := p.routingFromTrace(rs.ResourceSpans())
	if err != nil {
		return "", err
	}

	k := p.c.LocateKey(b)
	if k == nil {
		return "", errors.New("no member found")
	}
	logger.Debugf("traceID[%s] select endpoint: %s", string(b), k.String())
	return k.String(), nil
}

func (p *Picker) routingFromTrace(rs ptrace.ResourceSpansSlice) ([]byte, error) {
	if rs.Len() == 0 {
		return nil, errors.New("empty resource spans")
	}

	ils := rs.At(0).ScopeSpans()
	if ils.Len() == 0 {
		return nil, errors.New("empty scope spans")
	}

	spans := ils.At(0).Spans()
	if spans.Len() == 0 {
		return nil, errors.New("empty spans")
	}

	tid := spans.At(0).TraceID().HexString()
	return []byte(tid), nil
}

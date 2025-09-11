// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	GRPC  = "grpc"
	HTTP  = "http"
	HTTPS = "https"
)

type BackendRef struct {
	Address  string
	Protocol string
}

type endpointSet struct {
	allBackendList func() []*BackendRef
	updateMtx      sync.Mutex
	endpointsMtx   sync.RWMutex

	gRPCInfoCallTimeout time.Duration

	dialOpts  []grpc.DialOption
	endpoints map[string]*endpointRef
}

func NewEndpointSet(
	allBackendList func() []*BackendRef,
	dialOpts []grpc.DialOption,
) *endpointSet {
	if allBackendList == nil {
		allBackendList = func() []*BackendRef {
			return nil
		}
	}

	eps := &endpointSet{
		allBackendList:      allBackendList,
		dialOpts:            dialOpts,
		gRPCInfoCallTimeout: 5 * time.Second,
		endpoints:           make(map[string]*endpointRef),
	}
	return eps
}

func (e *endpointSet) getEndPointRef(ctx context.Context, protocol, address string) *endpointRef {
	er := &endpointRef{
		ctx:      ctx,
		address:  address,
		protocol: protocol,
		clients:  &endpointClients{},
	}
	switch protocol {
	case GRPC:
		conn, err := grpc.DialContext(ctx, address, e.dialOpts...)
		if err != nil {
			log.Errorf(ctx, "connect endpoint with %s %s error %s", address, protocol, err.Error())
			return nil
		}
		er.cc = conn
		er.clients.timeSeries = remote.NewQueryTimeSeriesServiceClient(conn)
		return er
	default:
		return nil
	}
}

func (e *endpointSet) getActiveEndpoints(ctx context.Context, endpoints map[string]*endpointRef) map[string]*endpointRef {
	var (
		activeEndpoints = make(map[string]*endpointRef, len(endpoints))
		mtx             sync.Mutex
		wg              sync.WaitGroup

		endpointAddrSet = make(map[string]struct{})
	)

	for _, b := range e.allBackendList() {
		if _, ok := endpointAddrSet[b.Address]; ok {
			continue
		}

		endpointAddrSet[b.Address] = struct{}{}
		wg.Add(1)
		go func(b *BackendRef) {
			defer wg.Done()
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, e.gRPCInfoCallTimeout)
			defer cancel()

			er, ok := endpoints[b.Address]
			if !ok {
				er = e.getEndPointRef(ctx, b.Protocol, b.Address)
			}
			mtx.Lock()
			defer mtx.Unlock()

			if er != nil {
				activeEndpoints[b.Address] = er
			}
		}(b)
	}
	wg.Wait()

	return activeEndpoints
}

func (e *endpointSet) Update(ctx context.Context) {
	e.updateMtx.Lock()
	defer e.updateMtx.Unlock()

	e.endpointsMtx.RLock()
	endpoints := make(map[string]*endpointRef, len(e.endpoints))
	for addr, er := range e.endpoints {
		endpoints[addr] = er
	}
	e.endpointsMtx.RUnlock()

	activeEndpoints := e.getActiveEndpoints(ctx, endpoints)

	for addr, er := range endpoints {
		if _, ok := activeEndpoints[addr]; ok {
			continue
		}

		log.Infof(ctx, "delete endpoint %s with address: %s, protocol: %s", addr, er.address, er.protocol)
		er.Close()
		delete(endpoints, addr)
	}

	for addr, er := range activeEndpoints {
		if _, ok := endpoints[addr]; ok {
			continue
		}

		log.Infof(ctx, "connect endpoint %s with address: %s, protocol: %s", addr, er.address, er.protocol)
		endpoints[addr] = er
	}

	if len(endpoints) > 0 {
		log.Infof(ctx, "old: %+v(%d) => new: %+v(%d)", endpoints, len(endpoints), activeEndpoints, len(activeEndpoints))

		e.endpointsMtx.Lock()
		e.endpoints = endpoints
		e.endpointsMtx.Unlock()
	}
}

func (e *endpointSet) Close() {
	e.endpointsMtx.Lock()
	defer e.endpointsMtx.Unlock()

	for _, er := range e.endpoints {
		er.Close()
	}
	e.endpoints = map[string]*endpointRef{}
}

type endpointRef struct {
	ctx context.Context

	cc *grpc.ClientConn

	clients *endpointClients

	address  string
	protocol string
}

func (er *endpointRef) Close() {
	if er.cc != nil {
		err := er.cc.Close()
		if err == nil {
			return
		}
		if errors.Is(err, os.ErrClosed) {
			return
		}
		log.Warnf(er.ctx, "detected close error %s", err.Error())
	}
}

type endpointClients struct {
	timeSeries remote.QueryTimeSeriesServiceClient
}

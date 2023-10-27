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
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cluster/pb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/batchspliter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type grpcClient interface {
	name() string
	close() error
	forwardTraces(ctx context.Context, traces ptrace.Traces) error
}

func wrapTracesRequest(traces ptrace.Traces) (*pb.ForwardRequest, error) {
	body, err := ptrace.NewProtoMarshaler().MarshalTraces(traces)
	if err != nil {
		return nil, err
	}

	return &pb.ForwardRequest{
		RecordType: define.RecordTraces.S(),
		Body:       body,
	}, nil
}

// remoteGrpcClient 真实的远程 grpc 链接
type remoteGrpcClient struct {
	conn   *grpc.ClientConn
	client pb.ClusterClient
}

func newRemoteClient(endpoint string) (grpcClient, error) {
	conn, err := grpc.Dial(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	client := pb.NewClusterClient(conn)
	return &remoteGrpcClient{
		conn:   conn,
		client: client,
	}, nil
}

func (cli *remoteGrpcClient) name() string {
	return "remote"
}

func (cli *remoteGrpcClient) close() error {
	return cli.conn.Close()
}

func (cli *remoteGrpcClient) forwardTraces(ctx context.Context, traces ptrace.Traces) error {
	req, err := wrapTracesRequest(traces)
	if err != nil {
		return err
	}

	_, err = cli.client.Forward(ctx, req)
	return err
}

// localGrpcClient 虚拟的本机 grpc 链接
type localGrpcClient struct{}

func newLocalGrpcClient() grpcClient {
	return localGrpcClient{}
}

func (localGrpcClient) name() string {
	return "local"
}

func (localGrpcClient) close() error {
	return nil
}

func (localGrpcClient) forwardTraces(ctx context.Context, traces ptrace.Traces) error {
	req, err := wrapTracesRequest(traces)
	if err != nil {
		return err
	}

	_, err = cluster.Forward(ctx, req)
	return err
}

type Client struct {
	conf     Config
	stop     chan struct{}
	mut      sync.RWMutex
	clients  map[string]grpcClient
	notReady map[string]struct{} // 未就绪的 endpoints
	resolver Resolver
	picker   *Picker
}

func NewClient(conf Config) *Client {
	cc := &Client{
		conf:     conf,
		stop:     make(chan struct{}, 1),
		clients:  make(map[string]grpcClient),
		notReady: map[string]struct{}{},
		resolver: NewResolver(conf.ResolverConfig),
		picker:   NewPicker(),
	}

	go cc.run()
	go cc.tryActive()
	return cc
}

func (c *Client) ForwardTraces(traces ptrace.Traces) error {
	batch := batchspliter.SplitTraces(traces)
	for i := 0; i < len(batch); i++ {
		endpoint, err := c.picker.PickTraces(batch[i])
		if err != nil {
			return err
		}

		client := c.getClient(endpoint)
		if client == nil {
			return errors.New("no client found")
		}
		if err := client.forwardTraces(context.Background(), batch[i]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) getClient(ep string) grpcClient {
	c.mut.RLock()
	defer c.mut.RUnlock()

	return c.clients[ep]
}

func (c *Client) run() {
	for {
		select {
		case <-c.stop:
			return
		case event := <-c.resolver.Watch():
			logger.Infof("handle event: %+v", event)
			c.handleEvent(event)
		}
	}
}

func (c *Client) handleEvent(event Event) {
	c.mut.Lock()
	defer c.mut.Unlock()

	switch event.Type {
	case EventTypeDelete:
		// 清理 member
		c.picker.RemoveMember(event.Endpoint)
		delete(c.notReady, event.Endpoint)
		client, ok := c.clients[event.Endpoint]
		if !ok {
			return
		}

		// 清理 client
		delete(c.clients, event.Endpoint)
		if err := client.close(); err != nil {
			logger.Errorf("failed to close client, endpoint=%v, err=%v", event.Endpoint, err)
		}

	case EventTypeAdd:
		var client grpcClient
		var err error

		switch event.Endpoint {
		case c.conf.ResolverConfig.Identifier:
			client = newLocalGrpcClient()
			logger.Infof("create local grpc client by identifier: %v", c.conf.ResolverConfig.Identifier)
		default:
			logger.Infof("create remote grpc client, endpoint=%v", event.Endpoint)
			client, err = newRemoteClient(event.Endpoint)
		}

		if err != nil {
			logger.Errorf("failed to create client, endpoint=%v, err=%v", event.Endpoint, err)
			c.notReady[event.Endpoint] = struct{}{}
			return
		}
		c.clients[event.Endpoint] = client
		c.picker.AddMember(event.Endpoint)
	}
}

func (c *Client) tryActive() {
	ticker := time.NewTicker(time.Second * 3)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return

		case <-ticker.C:
			c.mut.Lock()
			for ep := range c.notReady {
				logger.Debugf("try to active client, endpoint=%v", ep)
				client, err := newRemoteClient(ep)
				if err != nil {
					logger.Errorf("try to active client failed, endpoint=%v, err=%v", ep, err)
					continue
				}
				c.clients[ep] = client
				delete(c.notReady, ep)
			}
			c.mut.Unlock()
		}
	}
}

func (c *Client) Stop() error {
	close(c.stop)

	var errs []error
	for ep, client := range c.clients {
		logger.Debugf("client controller shouting down endpoint: %s", ep)
		if err := client.close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

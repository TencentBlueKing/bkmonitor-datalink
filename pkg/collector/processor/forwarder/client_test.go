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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/generator"
)

func TestLocalClient(t *testing.T) {
	client := newLocalGrpcClient()
	assert.Equal(t, "local", client.name())

	err := client.forwardTraces(context.Background(), ptrace.NewTraces())
	assert.NoError(t, err)
	assert.NoError(t, client.close())
	select {
	case <-cluster.Records():
	default:
	}
}

func TestRemoteClient(t *testing.T) {
	// server
	content := `
cluster:
  disabled: false
  address: localhost:65101
`
	conf := confengine.MustLoadConfigContent(content)
	server, err := cluster.NewServer(conf)
	assert.NoError(t, err)
	assert.NoError(t, server.Start())
	defer server.Stop()

	// client
	client, err := newRemoteClient("localhost:65101")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, "remote", client.name())

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})
	traces := g.Generate()
	err = client.forwardTraces(context.Background(), traces)
	assert.NoError(t, err)
	assert.NoError(t, client.close())
	select {
	case <-cluster.Records():
	default:
	}
}

func TestClient(t *testing.T) {
	client := NewClient(Config{ResolverConfig{
		Type:       resolverTypeStatic,
		Identifier: ":1001",
		Endpoints:  []string{":1001"},
	}})
	time.Sleep(time.Millisecond * 100)

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})
	traces := g.Generate()
	err := client.ForwardTraces(traces)
	assert.NoError(t, err)

	client.resolver.(*staticResolver).notifier.Sync([]string{})
	time.Sleep(time.Millisecond * 100)
}

func TestRemoteClientChaos(t *testing.T) {
	sig := make(chan struct{}, 2)
	go func() {
		<-sig
		// server
		content := `
cluster:
  disabled: false
  address: localhost:65101
`
		conf := confengine.MustLoadConfigContent(content)
		server, _ := cluster.NewServer(conf)
		assert.NoError(t, server.Start())
		<-sig
		server.Stop()
	}()

	stop := make(chan struct{})
	go func() {
		select {
		case <-stop:
			return
		case <-cluster.Records():
		}
	}()

	// client
	client, _ := newRemoteClient("localhost:65101")
	time.Sleep(time.Millisecond * 50)
	defer client.close()

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})

	go func() {
		time.Sleep(time.Millisecond * 500)
		sig <- struct{}{}
	}()

	for i := 0; i < 30; i++ {
		time.Sleep(time.Millisecond * 50)
		traces := g.Generate()
		err := client.forwardTraces(context.Background(), traces)
		t.Logf("chaos client err: %v", err)

	}
	close(stop)
	sig <- struct{}{}
}

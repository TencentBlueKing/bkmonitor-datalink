// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package forwarder

type EventType string

const (
	EventTypeAdd    EventType = "add"
	EventTypeDelete EventType = "delete"
)

type Event struct {
	Type     EventType
	Endpoint string
}

type Resolver interface {
	Type() string
	Watch() <-chan Event
	Stop() error
}

const (
	resolverTypeNoop   = "noop"
	resolverTypeStatic = "static"
)

func NewResolver(conf ResolverConfig) Resolver {
	switch conf.Type {
	case resolverTypeStatic:
		return newStaticResolver(conf)
	default:
		return newNoopResolver()
	}
}

// staticResolver 静态 resolver 实现
type staticResolver struct {
	notifier *EndpointNotifier
}

func newStaticResolver(conf ResolverConfig) Resolver {
	notifier := NewEventNotifier()
	go notifier.Sync(conf.Endpoints)
	return &staticResolver{
		notifier: notifier,
	}
}

func (sr *staticResolver) Type() string {
	return resolverTypeStatic
}

func (sr *staticResolver) Watch() <-chan Event {
	return sr.notifier.Watch()
}

func (sr *staticResolver) Stop() error {
	sr.notifier.Stop()
	return nil
}

// noopResolver resolver 空实现
type noopResolver struct {
	ch chan Event
}

func newNoopResolver() Resolver {
	return &noopResolver{
		ch: make(chan Event, 1),
	}
}

func (nr *noopResolver) Type() string {
	return resolverTypeNoop
}

func (nr *noopResolver) Watch() <-chan Event {
	return nr.ch
}

func (nr *noopResolver) Stop() error {
	close(nr.ch)
	return nil
}

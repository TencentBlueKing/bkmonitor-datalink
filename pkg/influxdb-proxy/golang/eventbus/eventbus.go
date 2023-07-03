// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventbus

import (
	"github.com/asaskevich/EventBus"
)

// Bus : Main event bus
var Bus = EventBus.New()

// Publish : executes callback defined for a topic.
// Any additional argument will be transferred to the callback
func Publish(topic string, args ...interface{}) {
	Bus.Publish(topic, args...)
}

// Subscribe subscribes to a topic.
// Returns error if `fn` is not a function.
func Subscribe(topic string, fn interface{}) error {
	return Bus.Subscribe(topic, fn)
}

// SubscribeAsync subscribes to a topic with an asynchronous callback
// Transactional determines whether subsequent callbacks for a topic are
// run serially (true) or concurrently (false)
// Returns error if `fn` is not a function.
func SubscribeAsync(topic string, fn interface{}, transactional bool) error {
	return Bus.SubscribeAsync(topic, fn, transactional)
}

// SubscribeOnce subscribes to a topic once. Handler will be removed after executing.
// Returns error if `fn` is not a function.
func SubscribeOnce(topic string, fn interface{}) error {
	return Bus.SubscribeOnce(topic, fn)
}

// SubscribeOnceAsync subscribes to a topic once with an asynchronous callback
// Handler will be removed after executing.
// Returns error if `fn` is not a function.
func SubscribeOnceAsync(topic string, fn interface{}) error {
	return Bus.SubscribeOnceAsync(topic, fn)
}

// Unsubscribe removes callback defined for a topic.
// Returns error if there are no callbacks subscribed to the topic.
func Unsubscribe(topic string, fn interface{}) error {
	return Bus.Unsubscribe(topic, fn)
}

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sidecar

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateOrUpdateFiles(t *testing.T) {
	sm := &secretManager{
		files:  make(map[string]map[string][]byte),
		events: make(chan configFile, 1024),
	}

	secrets := []struct {
		name string
		data map[string][]byte
	}{
		{
			name: "secret1",
			data: map[string][]byte{
				"token1.conf": []byte("foo"),
				"token2.conf": []byte("bar"),
			},
		},
		{
			name: "secret1",
			data: map[string][]byte{
				"token1.conf": []byte("foo"),
			},
		},
		{
			name: "secret1",
			data: map[string][]byte{
				"token1.conf": []byte("bar"),
			},
		},
		{
			name: "secret2",
			data: map[string][]byte{
				"token3.conf": []byte("foz"),
			},
		},
		{
			name: "secret1",
			data: map[string][]byte{},
		},
	}

	expectedEvents := []configFile{
		{
			name:   "token1.conf",
			action: actionCreateOrUpdate,
			data:   []byte("foo"),
		},
		{
			name:   "token2.conf",
			action: actionCreateOrUpdate,
			data:   []byte("bar"),
		},
		{
			name:   "token2.conf",
			action: actionDelete,
		},
		{
			name:   "token1.conf",
			action: actionCreateOrUpdate,
			data:   []byte("bar"),
		},
		{
			name:   "token3.conf",
			action: actionCreateOrUpdate,
			data:   []byte("foz"),
		},
		{
			name:   "token1.conf",
			action: actionDelete,
		},
	}

	var events []configFile
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for e := range sm.Watch() {
			events = append(events, e)
		}
		wg.Done()
	}()

	for _, c := range secrets {
		sm.createOrUpdateFiles(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: c.name,
			},
			Data: c.data,
		})
	}

	close(sm.events)
	wg.Wait()

	for i, expected := range expectedEvents {
		event := events[i]
		assert.Equal(t, expected.action, event.action)
		assert.Equal(t, expected.name, event.name)
		assert.Equal(t, expected.data, event.data)
	}
}

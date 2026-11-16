// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

func TestSetInstanceRejectsAddressesWithoutHost(t *testing.T) {
	t.Cleanup(consul.Close)
	for _, tc := range []struct {
		address string
		reason  string
	}{
		{"", consul.ReasonAddressNotConfigured},
		{"   ", consul.ReasonAddressNotConfigured},
		{":8500", consul.ReasonAddressMissingHost},
		{"http://:8500", consul.ReasonAddressMissingHost},
		{"https://:8501", consul.ReasonAddressMissingHost},
		{"http://", consul.ReasonAddressMissingHost},
		{"http://:8500/proxy", consul.ReasonAddressMissingHost},
	} {
		err := consul.SetInstance(context.Background(), "", tc.address, "", "", "")
		var addressErr *consul.AddressError
		if !errors.As(err, &addressErr) || addressErr.Reason != tc.reason {
			t.Errorf("address %q: want %s, got %v", tc.address, tc.reason, err)
			continue
		}
		if current := consul.AddressUnavailable(); current == nil || current.Reason != tc.reason {
			t.Errorf("address %q: the rejection must stay visible, got %v", tc.address, current)
		}
		// No client exists, so every call reports the same reason without dialing.
		if _, err := consul.GetKVData("any/key"); !errors.As(err, &addressErr) || addressErr.Reason != tc.reason {
			t.Errorf("address %q: KV read must report %s, got %v", tc.address, tc.reason, err)
		}
		if _, err := consul.WatchChange(context.Background(), "any/key"); !errors.As(err, &addressErr) {
			t.Errorf("address %q: watch must report the rejection, got %v", tc.address, err)
		}
		if _, err := consul.GetStorageInfoContext(context.Background()); !errors.As(err, &addressErr) {
			t.Errorf("address %q: Storage read must report the rejection, got %v", tc.address, err)
		}
	}
}

func TestSetInstanceAcceptsAddressesWithHost(t *testing.T) {
	t.Cleanup(consul.Close)
	for _, address := range []string{
		"127.0.0.1:8500",
		"consul.example",
		"http://192.0.2.10:8500",
		"https://consul.example",
		"http://192.0.2.10:8500/proxy",
		"[::1]:8500",
	} {
		if err := consul.SetInstance(context.Background(), "", address, "", "", ""); err != nil {
			t.Errorf("address %q must create a client: %v", address, err)
		}
		if current := consul.AddressUnavailable(); current != nil {
			t.Errorf("address %q must clear the previous rejection, got %v", address, current)
		}
	}
	// A rejected address replaces the previous client instead of reusing it.
	var addressErr *consul.AddressError
	_ = consul.SetInstance(context.Background(), "", ":8500", "", "", "")
	if _, err := consul.GetKVData("any/key"); !errors.As(err, &addressErr) || addressErr.Reason != consul.ReasonAddressMissingHost {
		t.Fatalf("a rejected address must not keep the previous client: %v", err)
	}
	// Close forgets the rejection together with the client.
	_ = consul.SetInstance(context.Background(), "", "", "", "", "")
	consul.Close()
	if current := consul.AddressUnavailable(); current != nil {
		t.Fatalf("close must clear the rejection, got %v", current)
	}
	if _, err := consul.GetKVData("any/key"); err == nil || errors.As(err, &addressErr) {
		t.Fatalf("a closed client is uninitialized, not rejected: %v", err)
	}
}

// A unix socket address has no host by design and must still reach Consul.
func TestSetInstanceUsesUnixSocketAddress(t *testing.T) {
	dir, err := os.MkdirTemp("", "uq-consul")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "consul.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"Key": "socket/key", "Value": []byte("from-socket"), "ModifyIndex": 1}})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(consul.Close)

	if err := consul.SetInstance(context.Background(), "", "unix://"+socket, "", "", ""); err != nil {
		t.Fatalf("unix socket address must create a client: %v", err)
	}
	value, err := consul.GetKVData("socket/key")
	if err != nil || string(value) != "from-socket" || requests.Load() != 1 {
		t.Fatalf("KV read must go through the socket, got value=%q err=%v requests=%d", value, err, requests.Load())
	}
}

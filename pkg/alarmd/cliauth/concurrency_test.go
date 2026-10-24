// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"net/http"
	"sync"
	"testing"
)

// Authorization requests arriving together are all served: the routes keep
// a rate per minute against a client looping without pause, and no count of
// requests in flight. Before, a fifth request in flight was refused as
// auth_busy.
func TestAuthorizationRequestsArrivingTogetherAreAllServed(t *testing.T) {
	m := newTestManager(t, startRedis(t))
	const together = 64
	codes := make([]int, together)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range codes {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			codes[i] = authRequest(m, http.MethodPost, grantsPath, `{"confirm":true}`, true).Code
		}()
	}
	start.Done()
	done.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("request %d answered %d, want every one served", i, code)
		}
	}
}

// Many people and agents logging in within one minute are all served.
func TestManyLoginsInAMinuteAreAllServed(t *testing.T) {
	m := newTestManager(t, startRedis(t))
	for i := 0; i < 300; i++ {
		if code := authRequest(m, http.MethodPost, grantsPath, `{"confirm":true}`, true).Code; code != http.StatusOK {
			t.Fatalf("grant %d in one minute answered %d", i+1, code)
		}
	}
}

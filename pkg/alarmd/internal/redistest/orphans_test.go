// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

//go:build unix

package redistest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// A server whose owning test process is gone is shut down, and its record
// removed - asked to, or, given a password since it started, killed as the
// redis-server process titled with its address. A server whose owner is
// alive is left running and recorded; a record that no longer matches the
// server at its address - another directory there, as when the port went to
// a later server - leaves that server alone; and a record of a server that
// is gone is removed.
func TestAServerWhoseOwnerIsGoneIsShutDownAndNoOtherIs(t *testing.T) {
	ctx := context.Background()
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	deadOwner := gone.ProcessState.Pid()
	if !processGone(deadOwner) {
		t.Skipf("pid %d was reused before the check", deadOwner)
	}
	orphan, owned, foreign, locked := Start(t), Start(t), Start(t), Start(t)
	if err := locked.Client().ConfigSet(ctx, "requirepass", "orphan-test").Err(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	record := func(name string, owner int, server *Instance, serverDir string) string {
		server.mu.Lock()
		pid := server.command.Process.Pid
		server.mu.Unlock()
		if serverDir == "" {
			resolved, err := filepath.EvalSymlinks(server.dir)
			if err != nil {
				t.Fatal(err)
			}
			serverDir = resolved
		}
		body, _ := json.Marshal(registration{Owner: owner, Server: pid, Addr: server.Addr, Dir: serverDir})
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	orphanRecord := record("orphan", deadOwner, orphan, "")
	ownedRecord := record("owned", os.Getpid(), owned, "")
	foreignRecord := record("foreign", deadOwner, foreign, "/not/this/servers/dir")
	lockedRecord := record("locked", deadOwner, locked, "")
	stoppedServer := Start(t)
	stoppedRecord := record("stopped", deadOwner, stoppedServer, "")
	stoppedServer.Stop()

	reaped := reapOrphans(dir)
	sort.Strings(reaped)
	want := []string{orphan.Addr, locked.Addr}
	sort.Strings(want)
	if !reflect.DeepEqual(reaped, want) {
		t.Fatalf("reaped %v, want only the orphans %v", reaped, want)
	}
	// The one given a password is not askable; it is read as a process and
	// killed, and its exit is what the instance's own wait sees.
	locked.mu.Lock()
	lockedExited := locked.exited
	locked.mu.Unlock()
	select {
	case <-lockedExited:
	case <-time.After(10 * time.Second): // fixed-wait: a SIGKILLed process is reaped in milliseconds
		t.Fatal("the orphan with a password is still running")
	}
	if err := orphan.Client().Ping(ctx).Err(); err == nil {
		t.Fatal("the orphan still answers")
	}
	for name, server := range map[string]*Instance{"owned": owned, "foreign": foreign} {
		if err := server.Client().Ping(ctx).Err(); err != nil {
			t.Fatalf("the %s server was shut down: %v", name, err)
		}
	}
	for path, kept := range map[string]bool{orphanRecord: false, ownedRecord: true, foreignRecord: false, lockedRecord: false, stoppedRecord: false} {
		if _, err := os.Stat(path); (err == nil) != kept {
			t.Fatalf("record %s kept = %v, want %v", filepath.Base(path), err == nil, kept)
		}
	}
}

// A started server is recorded under the registry with this process as its
// owner, and the record goes when the server stops.
func TestAStartedServerIsRecordedUntilItStops(t *testing.T) {
	server := Start(t)
	server.mu.Lock()
	path, pid := server.recorded, server.command.Process.Pid
	server.mu.Unlock()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no record for a started server: %v", err)
	}
	var entry registration
	if err := json.Unmarshal(body, &entry); err != nil || entry.Owner != os.Getpid() || entry.Server != pid || entry.Addr != server.Addr {
		t.Fatalf("record = %+v (%v), want owner %d, server %d at %s", entry, err, os.Getpid(), pid, server.Addr)
	}
	server.Stop()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the record outlived its server: %v", err)
	}
}

// The process fallback kills only a redis-server titled with the recorded
// address: not one at another address, which a reused pid would be, and not
// a process that is no redis-server.
func TestTheProcessFallbackKillsOnlyTheServerAtTheRecordedAddress(t *testing.T) {
	server := Start(t)
	server.mu.Lock()
	pid, exited := server.command.Process.Pid, server.exited
	server.mu.Unlock()
	if killServerProcess(pid, FreeAddress(t)) || killServerProcess(os.Getpid(), server.Addr) {
		t.Fatal("killed a process that is not the recorded server")
	}
	if err := server.Client().Ping(context.Background()).Err(); err != nil {
		t.Fatalf("the server was killed for another address: %v", err)
	}
	if !killServerProcess(pid, server.Addr) {
		t.Fatal("the recorded server was not killed")
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second): // fixed-wait: a SIGKILLed process is reaped in milliseconds
		t.Fatal("the recorded server is still running")
	}
}

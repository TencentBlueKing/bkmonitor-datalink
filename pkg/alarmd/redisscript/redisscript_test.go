// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package redisscript

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
)

func panicked(call func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	call()
	return ""
}

func TestWhatWouldMakeALabelWrongOrUnboundedDoesNotRegister(t *testing.T) {
	for _, name := range []string{"", "Upper", "1digit", "has-dash", "has space", strings.Repeat("a", 49), Other} {
		if panicked(func() { newRegistry().register(name, "return 1") }) == "" {
			t.Errorf("name %q registered", name)
		}
	}
	if panicked(func() { newRegistry().register(strings.Repeat("a", 48), "return 1") }) != "" {
		t.Error("the longest allowed name did not register")
	}
	if panicked(func() { newRegistry().register("empty", "") }) == "" {
		t.Error("a script with no text registered")
	}

	registry := newRegistry()
	registry.register("first", "return 1")
	if message := panicked(func() { registry.register("first", "return 2") }); !strings.Contains(message, "two different texts") {
		t.Errorf("one name for two texts: %q", message)
	}
	// One text under two names would read as whichever came last: the renew
	// that runs every round could all read as the restore.
	if message := panicked(func() { registry.register("second", "return 1") }); !strings.Contains(message, "same text") {
		t.Errorf("two names for one text: %q", message)
	}
	if script, _, _ := registry.resolve(CommandEval, []interface{}{"eval", "return 1"}); script != "first" {
		t.Errorf("after the refused name the text reads as %q", script)
	}
	if panicked(func() { registry.register("first", "return 1") }) != "" {
		t.Error("the same script registered twice, as a binary's two packages may, did not pass")
	}
}

func TestTheTableIsBounded(t *testing.T) {
	registry := newRegistry()
	for index := 0; index < MaxScripts; index++ {
		registry.register(fmt.Sprintf("script_%d", index), fmt.Sprintf("return %d", index))
	}
	if message := panicked(func() { registry.register("one_more", "return -1") }); !strings.Contains(message, "more than") {
		t.Errorf("past the bound: %q", message)
	}
	// A name already in the table is not one more.
	if panicked(func() { registry.register("script_0", "return 0") }) != "" {
		t.Error("registering a known script at the bound failed")
	}
}

func TestACommandReadsAsItsScript(t *testing.T) {
	registry := newRegistry()
	const body = "return redis.call('GET', KEYS[1])"
	registry.register("known", body)
	hash := redis.NewScript(body).Hash()
	for _, test := range []struct {
		name          string
		command       string
		args          []interface{}
		script        string
		scriptCommand string
		isScript      bool
	}{
		{"evalsha by the sha go-redis computes", "evalsha", []interface{}{"evalsha", hash, 1, "k"}, "known", CommandEvalSHA, true},
		{"eval by its text", "eval", []interface{}{"eval", body, 1, "k"}, "known", CommandEval, true},
		{"script load by its text", "script", []interface{}{"script", "load", body}, "known", CommandLoad, true},
		{"script load in any case", "script", []interface{}{"script", "LOAD", body}, "known", CommandLoad, true},
		{"an unknown sha", "evalsha", []interface{}{"evalsha", strings.Repeat("0", 40), 0}, Other, CommandEvalSHA, true},
		{"an unknown text", "eval", []interface{}{"eval", "return 2", 0}, Other, CommandEval, true},
		{"an unknown load", "script", []interface{}{"script", "load", "return 2"}, Other, CommandLoad, true},
		{"a sha where the text goes", "eval", []interface{}{"eval", hash, 0}, Other, CommandEval, true},
		{"a text where the sha goes", "evalsha", []interface{}{"evalsha", body, 0}, Other, CommandEvalSHA, true},
		{"no arguments", "evalsha", []interface{}{"evalsha"}, Other, CommandEvalSHA, true},
		{"an argument that is not a string", "eval", []interface{}{"eval", 7}, Other, CommandEval, true},
		{"load with no text", "script", []interface{}{"script", "load"}, "", "", false},
		{"script exists is not a script going out", "script", []interface{}{"script", "exists", hash}, "", "", false},
		{"script flush", "script", []interface{}{"script", "flush"}, "", "", false},
		{"a load word that is not a string", "script", []interface{}{"script", 7, body}, "", "", false},
		{"another command", "get", []interface{}{"get", body}, "", "", false},
		{"no command", "", nil, "", "", false},
	} {
		script, scriptCommand, isScript := registry.resolve(test.command, test.args)
		if script != test.script || scriptCommand != test.scriptCommand || isScript != test.isScript {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", test.name,
				script, scriptCommand, isScript, test.script, test.scriptCommand, test.isScript)
		}
	}
}

func TestNewReturnsTheScriptRedisWouldAndRegistersIt(t *testing.T) {
	const body = "return 'redisscript new test'"
	script := New("redisscript_new_test", body)
	if script.Hash() != redis.NewScript(body).Hash() {
		t.Fatal("the script's SHA-1 changed by being named")
	}
	if name, command, ok := Resolve("evalsha", []interface{}{"evalsha", script.Hash()}); name != "redisscript_new_test" || command != CommandEvalSHA || !ok {
		t.Fatalf("Resolve by the script's hash = (%q, %q, %v)", name, command, ok)
	}
	if Register("redisscript_register_test", "return 'redisscript register test'") != "return 'redisscript register test'" {
		t.Fatal("Register did not return the text")
	}
	names := Names()
	if len(names) != 2 || names[0] != "redisscript_new_test" || names[1] != "redisscript_register_test" {
		t.Fatalf("Names() = %v", names)
	}
}

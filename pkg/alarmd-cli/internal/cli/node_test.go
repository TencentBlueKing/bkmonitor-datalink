package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestNodeAsyncContractAndCursor(t *testing.T) {
	for _, state := range []string{"running", "failed"} {
		t.Run(state, func(t *testing.T) {
			p := fixtureProfile("http://fixture.test")
			p.Scope = sessionScope
			store := Store{t.TempDir()}
			_ = store.save(p, false)
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope}), 200), nil
				}
				result := map[string]any{"execution_id": "id-fixture", "state": state, "runner": map[string]any{"next_cursor": "cursor-42", "output": []any{map[string]any{"stream": "stdout", "data": "node evidence"}}}}
				out := envelope(p, "ok", result)
				if state == "failed" {
					out["status"] = "error"
					out["error"] = map[string]any{"code": "node_execution_failed", "message": "command exited nonzero"}
					result["exit_code"] = 7
				}
				return jsonHTTP(out, 200), nil
			})}
			code, out, _, _ := run(t, store, client, "", "invoke", "node.exec", "--env", p.EnvironmentID, "--input", `{"execution_id":"id-fixture"}`)
			want := 0
			if state == "failed" {
				want = 1
			}
			if code != want || calls != 2 {
				t.Fatalf("code=%d calls=%d result=%v", code, calls, out)
			}
			raw, err := os.ReadFile(stringField(objectField(out, "meta"), "result_file"))
			if err != nil || !strings.Contains(string(raw), "cursor-42") || !strings.Contains(string(raw), "node evidence") {
				t.Fatalf("lost async evidence: %s %v", raw, err)
			}
		})
	}
}

func TestNodeDisconnectRetainsExecutionIDWithoutReplay(t *testing.T) {
	p := fixtureProfile("http://fixture.test")
	p.Scope = sessionScope
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope}), 200), nil
		}
		return nil, context.DeadlineExceeded
	})}
	code, out, _, _ := run(t, store, client, "", "invoke", "node.exec", "--env", p.EnvironmentID, "--input", `{"execution_id":"id-fixture","stdin":"private-script"}`)
	if code != 1 || calls != 2 || objectField(out, "result")["execution_id"] != "id-fixture" {
		t.Fatalf("lost execution: %v", out)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), "node.execution.get") || strings.Contains(string(raw), "private-script") {
		t.Fatalf("unsafe recovery: %s", raw)
	}
}

func TestNodeExecLargeStdinUsesAdvertisedEnvelope(t *testing.T) {
	p := fixtureProfile("http://fixture.test")
	p.Scope = sessionScope
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	script := strings.Repeat("x", 1<<20)
	path := t.TempDir() + "/script.sh"
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTP(envelope(p, "ok", map[string]any{"effect": "exec", "required_scope": sessionScope, "request_max_bytes": 16 << 20, "input_schema": map[string]any{"properties": map[string]any{"stdin": map[string]any{"type": "string", "parameter_source": "stdin", "maxLength": 1 << 20}}}}), 200), nil
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if objectField(body, "params")["stdin"] != script {
			t.Error("large script truncated")
		}
		return jsonHTTP(envelope(p, "ok", map[string]any{"execution_id": "fixture", "state": "running"}), 200), nil
	})}
	code, out, _, _ := run(t, store, client, "", "invoke", "node.exec", "--env", p.EnvironmentID, "--input", `{"execution_id":"fixture"}`, "--script-file", path)
	if code != 0 || calls != 2 {
		t.Fatalf("large script refused: code=%d calls=%d %v", code, calls, out)
	}
}

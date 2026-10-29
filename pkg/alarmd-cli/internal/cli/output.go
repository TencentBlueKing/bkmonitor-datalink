// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const stdoutBudget = 20 << 10

func sensitiveKey(key string) bool {
	k := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	switch k {
	case "adminkey", "apikey", "privatekey", "authorization", "proxyauthorization", "accesstoken", "refreshtoken", "token", "grantsecret", "grant", "grantcode", "logincode", "password", "passwd", "secret", "clientsecret", "cookie", "setcookie", "assertion", "identityassertion", "jwt", "xbkapijwt", "headers", "requestheaders", "responseheaders", "credentials":
		return true
	}
	return false
}

// credentialBundle is a key whose structure is a credential by what it is:
// a header set, a credential set, a cookie jar, an authorization grant.
func credentialBundle(key string) bool {
	k := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	switch k {
	case "headers", "requestheaders", "responseheaders", "credentials", "cookie", "setcookie", "grant", "grantsecret", "grantcode":
		return true
	}
	return false
}

// redact leaves out a credential-named key's scalar value and reads a
// structure under one as a reference to where a credential is kept: a
// Kubernetes object keeps its Secret references - a volume's
// secret.secretName, a ServiceMonitor's basicAuth.password {name, key} -
// which are how a reader finds a dependency, and every other value under it
// is masked. A credential bundle is left out whole.
func redact(v any, secrets []string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			if credentialBundle(k) {
				continue
			}
			if sensitiveKey(k) {
				if structured(value) {
					out[redact(k, secrets).(string)] = reference(value, secrets)
				}
				continue
			}
			out[redact(k, secrets).(string)] = redact(value, secrets)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = redact(x[i], secrets)
		}
		return out
	case string:
		for _, s := range secrets {
			if s != "" {
				x = strings.ReplaceAll(x, s, "[REDACTED]")
			}
		}
		// A server must not emit authorization bundles, even in free text.
		if strings.Contains(x, "alarmd-login-v1.") {
			return "[REDACTED authorization bundle]"
		}
		return x
	default:
		return v
	}
}

// referenceFields are the fields a reference to a Secret has; the server's
// redaction (pkg/alarmd/sre/redact) holds the same table.
var referenceFields = map[string]bool{"secretName": true, "name": true, "key": true, "optional": true, "items": true,
	"path": true, "mode": true, "defaultMode": true, "namespace": true}

func sortedReferenceFields() []string {
	fields := make([]string, 0, len(referenceFields))
	for field := range referenceFields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// reference is a structure under a credential-named key: a reference's
// fields, walked the same way, and every other value - a field with another
// name, a list element that is not a structure - masked.
func reference(value any, secrets []string) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if !referenceFields[key] {
				out[key] = "[REDACTED]"
				continue
			}
			if structured(item) {
				out[key] = reference(item, secrets)
				continue
			}
			out[key] = redact(item, secrets)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			if structured(item) {
				out[i] = reference(item, secrets)
				continue
			}
			out[i] = "[REDACTED]"
		}
		return out
	default:
		return "[REDACTED]"
	}
}

func structured(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

func (a *App) print(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 1
	}
	if len(data)+1 > stdoutBudget {
		data = []byte(`{"status":"error","error":{"code":"output_too_large","message":"local output exceeds 20 KiB"}}`)
		_, _ = fmt.Fprintln(a.Out, string(data))
		return 1
	}
	if _, err := fmt.Fprintln(a.Out, string(data)); err != nil {
		return 1
	}
	return 0
}

func (a *App) fail(code, message string, exit int) int {
	a.print(map[string]any{"status": "error", "summary": message, "error": map[string]any{"code": code, "message": message}, "evidence": map[string]any{"complete": false, "limitations": []string{message}}, "next_call": []any{}})
	return exit
}

func confirmedExecRejection(response map[string]any, status int) bool {
	if status < 400 || stringField(objectField(response, "meta"), "channel_version") != channelVersion || response["result"] != nil {
		return false
	}
	code := stringField(objectField(response, "error"), "code")
	if status < 500 {
		return code != ""
	}
	// These named failures happen at the channel's authentication or local
	// availability gate, before the provider can run. A missing result after
	// execution (for example response_budget_exceeded) remains unknown.
	return code == "auth_store_unavailable" || code == "operation_unavailable" || code == "target_routing_unavailable"
}

func validateExecReceipt(operation string, response map[string]any) error {
	receipt := objectField(response, "result")
	if receipt == nil {
		return errors.New("execution response is missing an object receipt")
	}
	if operation != "pod.exec" {
		return nil
	}
	switch stringField(receipt, "remote_state") {
	case "remote_not_started", "remote_state_unknown", "remote_timeout_confirmed":
	case "remote_completed":
		exit, ok := receipt["exit_code"].(json.Number)
		if _, err := exit.Int64(); !ok || err != nil {
			return errors.New("completed Pod execution is missing its exit code")
		}
	case "remote_unsupervised":
		// Nothing in the target could end it: it ended with an exit code,
		// or this side cut its stream at the deadline.
		if exit, ok := receipt["exit_code"].(json.Number); ok {
			if _, err := exit.Int64(); err != nil {
				return errors.New("unsupervised Pod execution has an invalid exit code")
			}
		} else if receipt["stream_cut"] != true {
			return errors.New("unsupervised Pod execution has neither an exit code nor a cut stream")
		}
	case "":
		return errors.New("Pod execution receipt has no remote_state")
	default:
		// A state a newer server reports and this client does not know yet is
		// shown as the server wrote it, not refused: the receipt is still a
		// receipt, and refusing it would save a known execution as unknown.
	}
	return nil
}

// unknownExecution saves a bounded, redacted observation of a dispatched call.
// A decoded but rejected receipt stays explicitly unverified; malformed raw
// bytes are discarded. Scripts and argv stay represented only by their digest.
func (a *App) unknownExecution(operation string, params map[string]any, revision string, p Profile, started time.Time, receipt map[string]any, status int, cause string) int {
	body, _ := json.Marshal(channelBody("invoke", operation, params, revision))
	digest := sha256.Sum256(body)
	message := "Remote execution outcome is unconfirmed. Inspect the existing execution and target before an explicit retry; this CLI did not retry."
	result := map[string]any{"remote_state": "remote_state_unknown", "operation": operation, "request_digest": "sha256:" + hex.EncodeToString(digest[:]), "started_at": started.Format(time.RFC3339Nano), "finished_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if operation == "node.exec" || operation == "node.execution.cancel" {
		result["execution_id"] = params["execution_id"]
	}
	if target, exists := params["target"]; exists {
		result["target"] = target
	}
	if receipt != nil {
		result["unverified_response"] = receipt
	}
	response := map[string]any{"status": "error", "summary": message, "error": map[string]any{"code": "remote_state_unknown", "message": message}, "result": result,
		"evidence": map[string]any{"complete": false, "limitations": []string{message, cause}}, "meta": map[string]any{"environment_id": p.EnvironmentID, "http_status": status}, "next_call": []any{}}
	if operation == "node.exec" || operation == "node.execution.cancel" {
		response["next_call"] = []any{map[string]any{"operation": "node.execution.get", "params": map[string]any{"execution_id": params["execution_id"]}, "reason": "Query the same execution; do not replay the command."}}
	}
	return a.emitResponse(response, []string{p.AccessToken, p.RefreshToken}, http.StatusOK)
}

func (a *App) emitResponse(raw map[string]any, secrets []string, httpStatus int) int {
	result := redact(raw, secrets).(map[string]any)
	if httpStatus < 200 || httpStatus >= 300 {
		result["status"] = "error"
	}
	meta := objectField(result, "meta")
	if meta == nil {
		meta = map[string]any{}
		result["meta"] = meta
	}
	if httpStatus < 200 || httpStatus >= 300 {
		meta["http_status"] = httpStatus
	}
	path, err := a.saveEvidence(result)
	if err != nil {
		return a.fail("evidence_write_failed", err.Error(), 1)
	}
	data, _ := json.Marshal(result)
	code := 0
	if stringField(result, "status") == "partial" {
		code = 3
	}
	if stringField(result, "status") == "error" || httpStatus < 200 || httpStatus >= 300 {
		code = 1
	}
	if len(data)+1 > stdoutBudget {
		result = map[string]any{"status": result["status"], "summary": result["summary"], "evidence": result["evidence"], "next_call": result["next_call"], "meta": meta, "result_omitted": true}
		if raw["error"] != nil {
			result["error"] = redact(raw["error"], secrets)
		}
		compact, _ := json.Marshal(result)
		if len(compact)+1 > stdoutBudget {
			result = map[string]any{"status": result["status"], "summary": "Response exceeds stdout budget; read meta.result_file for complete redacted evidence.", "meta": map[string]any{"result_file": path}, "result_omitted": true}
		}
	}
	if a.print(result) != 0 {
		return 1
	}
	return code
}

// saveEvidence writes one redacted record atomically to the private
// evidence directory and names the file in its meta.result_file.
func (a *App) saveEvidence(result map[string]any) (string, error) {
	meta := objectField(result, "meta")
	if meta == nil {
		meta = map[string]any{}
		result["meta"] = meta
	}
	dir := filepath.Join(a.Store.Dir, "results")
	if err := secureDir(dir); err != nil {
		return "", errors.New("cannot create the private evidence directory")
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return "", errors.New("cannot create an evidence identifier")
	}
	path, err := filepath.Abs(filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+hex.EncodeToString(id)+".json"))
	if err != nil {
		return "", errors.New("cannot resolve evidence path")
	}
	meta["result_file"] = path
	data, err := json.Marshal(result)
	if err != nil || atomicWrite(path, append(data, '\n')) != nil {
		return "", errors.New("cannot save the complete redacted response")
	}
	return path, nil
}

// channelFailure checks a channel response for a failure alarmd did not
// answer on this CLI's terms, and reports it; false when the response is
// alarmd's to report as it is. A success has passed validateChannel
// already, and a refused session goes to the session's own handling
// whoever wrote it: logging in again is the answer either way. Otherwise
// the channel's meta decides, which the server writes on every answer:
// absent, a proxy or gateway in front of alarmd wrote the failure; present
// with another version, alarmd answered across a version this CLI does not
// speak, the same skew a success is refused for.
func (a *App) channelFailure(m map[string]any, status int) (int, bool) {
	if status >= 200 && status < 300 || status == http.StatusUnauthorized {
		return 0, false
	}
	switch version := stringField(objectField(m, "meta"), "channel_version"); version {
	case channelVersion:
		return 0, false
	case "":
		return a.gatewayFailure(status), true
	default:
		return a.channelVersionFailure(status, version, stringField(objectField(m, "error"), "code")), true
	}
}

// channelVersionFailure reports an alarmd failure answered on another
// channel version, with the code alarmd gave it.
func (a *App) channelVersionFailure(status int, version, code string) int {
	message := fmt.Sprintf("unsupported channel version; upgrade compatibility must be checked: "+
		"alarmd answered HTTP %d on %s, and this CLI speaks %s.", status, version, channelVersion)
	a.print(map[string]any{"status": "error", "summary": message,
		"error":    map[string]any{"code": "protocol_error", "message": message, "http_status": status, "server_error_code": code},
		"evidence": map[string]any{"complete": false, "limitations": []string{message}},
		"meta":     map[string]any{"http_status": status, "server_channel_version": version}, "next_call": []any{}})
	return 1
}

// gatewayFailure reports a failure a proxy or gateway in front of alarmd
// answered, by its HTTP status, and without its body: that body is someone
// else's page - help links, request ids, markup - and printed as the result
// it read as alarmd's answer, with no error code a script could branch on.
func (a *App) gatewayFailure(status int) int {
	message := fmt.Sprintf("HTTP %d came from a proxy or gateway in front of alarmd, not from alarmd; the call may not have reached it. "+
		"Retry; if it persists, check the environment's entry and the gateway in front of it.", status)
	a.print(map[string]any{"status": "error", "summary": message,
		"error":    map[string]any{"code": "gateway_error", "message": message, "http_status": status},
		"evidence": map[string]any{"complete": false, "limitations": []string{message}},
		"meta":     map[string]any{"http_status": status}, "next_call": []any{}})
	return 1
}

func protocolError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("server response rejected: " + err.Error())
}

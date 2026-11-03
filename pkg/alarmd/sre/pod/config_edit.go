// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

// OpEditConfigMapText replaces one piece of shown text inside one ConfigMap
// key, as a receipt and the audit name it.
const OpEditConfigMapText = "configmap.edit_text"

// A ConfigMap key is often a whole file - a config.yaml, an environment
// file - and a value inside it, a URL, has to be changed in place. The
// channel shows such a key masked (ConfigValueContract), so the key cannot
// be read, edited and written back whole: what comes back would be
// placeholders. The edit is made on the server instead, against the stored
// value, and only where the caller could see.
//
// Seeing is the rule, and it is checked on what is shown, never on the
// stored value alone. "old_text occurs once in the stored value" would answer
// a question about the masked parts: guess a masked secret's text and the
// answer says whether it was there. So the edit is decided on renderings:
//
//   - old_text must occur exactly once in the key as shown (the rendering
//     configmap.get gives: the masker, then the output redaction);
//   - of the places old_text occurs in the stored value, the one edited is
//     the first that, when replaced, changes the rendering by exactly that
//     substitution and nothing else.
//
// A place inside a masked value changes that value's size or fingerprint in
// its placeholder, or unmasks it; a place spanning a masked value and plain
// text does the same; a place in a comment, which is not shown, changes
// nothing shown. None of them renders as the substitution, so none counts,
// and whether one exists makes no difference to the answer. The shown
// occurrence always does. The answer therefore depends only on what the
// caller was shown, and the edit lands where the caller saw old_text.
//
// The same rendering check keeps key material out: new_text that would be
// masked (key material, or a value under a credential-like name) renders as a
// placeholder, not as itself, and is refused. new_text is also checked on its
// own first, so the common case is refused with its own words.

// The bounds of one edit.
const (
	maxEditTextBytes = 4096
	// maxEditCandidates bounds the stored occurrences of old_text tried,
	// each one rendering of the key. It is the one answer that can depend on
	// what is not shown: shown text preceded by more than this many hidden
	// copies of itself is refused as too common. That says only that hidden
	// text repeats a shown string hundreds of times, which no credential can.
	maxEditCandidates = 256
)

// EditChecked is a dry run whose every check held: nothing was written.
const EditChecked = "checked"

// The refusals of an edit, closed.
const (
	EditKeyNotFound        = "key_not_found"
	EditFingerprintChanged = "fingerprint_mismatch"
	EditNewTextNotPlain    = "new_text_not_plain"
	EditOldTextNotShown    = "old_text_not_shown_once"
	EditNotShownExactly    = "edit_not_shown_exactly"
)

// ConfigMapEditRequest names one key of one ConfigMap, the shown text to
// replace in it and its replacement, and the key's fingerprint as
// configmap.get showed it.
type ConfigMapEditRequest struct {
	Namespace           string `json:"namespace,omitempty"`
	Name                string `json:"name"`
	Key                 string `json:"key"`
	OldText             string `json:"old_text"`
	NewText             string `json:"new_text"`
	ExpectedFingerprint string `json:"expected_fingerprint"`
	DryRun              bool   `json:"dry_run,omitempty"`
}

// ConfigEditLine is one line of the key as shown after the edit.
type ConfigEditLine struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// ConfigMapEditReceipt is what one edit did, in sizes, places and
// fingerprints: never a stored value. ChangedLines are the stored value's
// lines (1-based); Shown is the changed line as configmap.get will now show
// it, numbered in that rendering. Write is the guarded patch's own receipt;
// a dry run has none.
type ConfigMapEditReceipt struct {
	Namespace         string           `json:"namespace"`
	Name              string           `json:"name"`
	UID               string           `json:"uid,omitempty"`
	Key               string           `json:"key"`
	DryRun            bool             `json:"dry_run,omitempty"`
	Outcome           string           `json:"outcome,omitempty"`
	Occurrences       int              `json:"occurrences"`
	ChangedLines      []int            `json:"changed_lines"`
	BeforeFingerprint string           `json:"before_fingerprint,omitempty"`
	AfterFingerprint  string           `json:"after_fingerprint,omitempty"`
	FingerprintScope  string           `json:"fingerprint_scope,omitempty"`
	Shown             []ConfigEditLine `json:"shown"`
	Write             *WriteReceipt    `json:"write,omitempty"`
	RedactionContract string           `json:"redaction_contract"`
	ValueContract     string           `json:"value_contract"`
	At                time.Time        `json:"at"`
}

func refuseEdit(code, message string) error { return &Error{Code: code, Message: message} }

func validateEdit(request ConfigMapEditRequest) error {
	switch {
	case len(validation.IsDNS1123Subdomain(request.Name)) != 0:
		return invalid("name must be a ConfigMap name")
	case request.Key == "" || len(validation.IsConfigMapKey(request.Key)) != 0:
		return invalid("key must be a ConfigMap key")
	case request.OldText == "" || len(request.OldText) > maxEditTextBytes || len(request.NewText) > maxEditTextBytes:
		return invalid(fmt.Sprintf("old_text must be given, and old_text and new_text at most %d bytes", maxEditTextBytes))
	case strings.ContainsAny(request.OldText+request.NewText, "\r\n"):
		return invalid("old_text and new_text are each one line: edit a multi-line value one line at a time")
	case request.OldText == request.NewText:
		return invalid("new_text is old_text: nothing to change")
	case request.ExpectedFingerprint == "":
		return invalid("expected_fingerprint is required: the key's fingerprint as configmap.get showed it")
	}
	return nil
}

// EditConfigMapText replaces old_text with new_text in one ConfigMap key
// where configmap.get showed it, if the key still holds what was read: its
// fingerprint is checked here and its stored value, resourceVersion and uid
// are tested by the apiserver in the same patch, which applies all of it or
// none (test_failed).
func (p *Provider) EditConfigMapText(ctx context.Context, request ConfigMapEditRequest) (ConfigMapEditReceipt, error) {
	receipt := ConfigMapEditReceipt{Namespace: request.Namespace, Name: request.Name, Key: request.Key, DryRun: request.DryRun,
		ChangedLines: []int{}, Shown: []ConfigEditLine{}, RedactionContract: RedactionContract, ValueContract: ConfigValueContract, At: p.options.Now().UTC()}
	if p.options.DryRun == nil || p.options.Writer == nil {
		return receipt, &Error{Code: CodeAPIError, Message: "Kubernetes write adapter is unavailable"}
	}
	if err := validateEdit(request); err != nil {
		return receipt, err
	}
	namespace, err := p.namespace(request.Namespace)
	if err != nil {
		return receipt, err
	}
	receipt.Namespace = namespace
	live, err := p.readConfigMapObject(ctx, namespace, request.Name)
	if err != nil {
		return receipt, err
	}
	receipt.UID = string(live.GetUID())
	data, _ := live.Object["data"].(map[string]any)
	stored, isText := data[request.Key].(string)
	if !isText {
		return receipt, refuseEdit(EditKeyNotFound, "the ConfigMap has no text key "+bound(request.Key))
	}
	fingerprints := p.newFingerprints()
	receipt.FingerprintScope = fingerprints.Scope()
	receipt.BeforeFingerprint = fingerprints.sum([]byte(stored))
	if receipt.BeforeFingerprint != request.ExpectedFingerprint {
		return receipt, refuseEdit(EditFingerprintChanged, "the key's fingerprint is not expected_fingerprint: it changed since it was read, "+
			"or it was read under another fingerprint key (this answer's scope is "+receipt.FingerprintScope+"); read it again with configmap.get")
	}
	if strings.ContainsAny(request.NewText, "\r\n") || credentialShaped(request.NewText) {
		return receipt, refuseEdit(EditNewTextNotPlain, "new_text spans lines or would be masked as shaped like a credential: "+
			"a credential goes in through secret handling, not an edit of shown text")
	}
	render := func(value string) string {
		masker := &configMasker{fingerprints: fingerprints}
		shown, _ := p.redactor.bounded(masker.value(request.Key, value), math.MaxInt64)
		return shown
	}
	before := render(stored)
	if strings.Count(before, request.OldText) != 1 {
		return receipt, refuseEdit(EditOldTextNotShown, fmt.Sprintf("old_text occurs %d times in the key as shown: give text that configmap.get shows exactly once",
			strings.Count(before, request.OldText)))
	}
	shownAt := strings.Index(before, request.OldText)
	want := before[:shownAt] + request.NewText + before[shownAt+len(request.OldText):]
	// The places are tried in order and the first that renders as the
	// substitution is the one: a place in a masked value or a comment never
	// does, so no hidden place is ever taken before the shown one.
	edited, at := "", -1
	tried := 0
	for index := strings.Index(stored, request.OldText); index >= 0; {
		tried++
		if tried > maxEditCandidates {
			break
		}
		candidate := stored[:index] + request.NewText + stored[index+len(request.OldText):]
		if render(candidate) == want {
			edited, at = candidate, index
			break
		}
		next := strings.Index(stored[index+1:], request.OldText)
		if next < 0 {
			break
		}
		index += 1 + next
	}
	if at < 0 {
		return receipt, refuseEdit(EditNotShownExactly, "replacing old_text does not change the key as shown by exactly that text: "+
			"it is not where configmap.get shows it, new_text would be masked or would change the file's structure, or old_text is too common; "+
			"give longer shown text around the value")
	}
	receipt.Occurrences = 1
	receipt.ChangedLines = []int{strings.Count(stored[:at], "\n") + 1}
	receipt.AfterFingerprint = fingerprints.sum([]byte(edited))
	shownLine := strings.Count(want[:shownAt], "\n")
	receipt.Shown = []ConfigEditLine{{Line: shownLine + 1, Text: strings.Split(want, "\n")[shownLine]}}
	if request.DryRun {
		receipt.Outcome = EditChecked
		return receipt, nil
	}
	oldValue, _ := json.Marshal(stored)
	newValue, _ := json.Marshal(edited)
	version, _ := json.Marshal(live.GetResourceVersion())
	path := "/data/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(request.Key)
	write, err := p.PatchObject(ctx, ObjectPatchRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: namespace, Name: request.Name, UID: receipt.UID,
		Patch: []PatchOp{{Op: "test", Path: "/metadata/resourceVersion", Value: version}, {Op: "test", Path: path, Value: oldValue}, {Op: "replace", Path: path, Value: newValue}}})
	receipt.Write = &write
	if len(write.Objects) == 1 {
		receipt.Outcome = write.Objects[0].Outcome
	}
	return receipt, err
}

// readConfigMapObject reads one ConfigMap as the writes read objects.
func (p *Provider) readConfigMapObject(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": name, "namespace": namespace}}}
	resource, outcome, message := p.gate(object, "", WriteRBACNotWritable, WriteNotGrantedKind)
	if outcome != "" {
		return nil, &Error{Code: outcome, Message: message}
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	live, err := p.options.DryRun.GetResource(ctx, resource, namespace, name)
	if apierrors.IsNotFound(err) {
		return nil, &Error{Code: CodeNotFound, Message: "no ConfigMap " + namespace + "/" + name}
	}
	if err != nil {
		return nil, apiError(err)
	}
	return live, nil
}

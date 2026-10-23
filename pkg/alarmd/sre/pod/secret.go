// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// OpReadSecret is the Secret read, as a receipt and the audit name it.
const OpReadSecret = "secret.get"

// MaxSecretKeys bounds the keys one read returns.
const MaxSecretKeys = 64

// SecretReader is the one Kubernetes read a Secret read makes: a get by
// name. The ServiceAccount may get only the Secrets the deployment's
// whitelist names, so a Secret outside it is the apiserver's refusal.
type SecretReader interface {
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)
}

// SecretRequest names one Secret. Reveal asks for the values themselves;
// without it a read returns each key's size and digest and no value.
type SecretRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Reveal    bool   `json:"reveal,omitempty"`
}

// SecretKey is one key of a Secret: its value's size and SHA-256, and the
// value only when the request asked for it.
type SecretKey struct {
	Key    string `json:"key"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Value  string `json:"value,omitempty"`
}

// SecretReceipt is what one Secret read returned. Revealed records that the
// request asked for the values; Truncated that keys past MaxSecretKeys were
// left out.
type SecretReceipt struct {
	Namespace         string      `json:"namespace"`
	Name              string      `json:"name"`
	UID               string      `json:"uid"`
	Type              string      `json:"type"`
	Keys              []SecretKey `json:"keys"`
	Revealed          bool        `json:"revealed"`
	Truncated         bool        `json:"truncated"`
	RedactionContract string      `json:"redaction_contract"`
	At                time.Time   `json:"at"`
}

// ReadSecret reads one Secret the deployment's whitelist names. A value is
// returned only when the request reveals it, and then masked as a ConfigMap
// read masks it: whole under a credential-named key, otherwise through the
// output redaction (credentials the deployment knows, credential fields, URL
// userinfo).
func (p *Provider) ReadSecret(ctx context.Context, request SecretRequest) (SecretReceipt, error) {
	result := SecretReceipt{Namespace: request.Namespace, Name: request.Name, Revealed: request.Reveal, Keys: []SecretKey{}, RedactionContract: RedactionContract, At: p.options.Now().UTC()}
	if p.options.Secrets == nil {
		return result, &Error{Code: CodeAPIError, Message: "Kubernetes Secret adapter is unavailable"}
	}
	namespace, err := p.namespace(request.Namespace)
	if err != nil {
		return result, err
	}
	result.Namespace = namespace
	if len(validation.IsDNS1123Subdomain(request.Name)) != 0 {
		return result, invalid("name must be a Secret name")
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	secret, err := p.options.Secrets.GetSecret(ctx, namespace, request.Name)
	if err != nil {
		return result, apiError(err)
	}
	result.UID, result.Type = string(secret.UID), string(secret.Type)
	keys := make([]string, 0, len(secret.Data))
	for key := range secret.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > MaxSecretKeys {
		keys, result.Truncated = keys[:MaxSecretKeys], true
	}
	for _, key := range keys {
		value := secret.Data[key]
		digest := sha256.Sum256(value)
		entry := SecretKey{Key: key, Bytes: len(value), SHA256: hex.EncodeToString(digest[:])}
		if request.Reveal {
			// The same masking a ConfigMap read applies: a value under a
			// credential-named key is masked whole, the rest goes through the
			// output redaction. Hosts, ports and user names show; a
			// credential is compared by its size and digest.
			shown := string(value)
			if credentialKey.MatchString(key) && shown != "" {
				shown = "<redacted>"
			}
			redacted, cut := p.redactor.bounded(shown, p.options.MaxOutputBytes)
			entry.Value = redacted
			result.Truncated = result.Truncated || cut
		}
		result.Keys = append(result.Keys, entry)
	}
	return result, nil
}

// OpReadConfigMap is the ConfigMap read, as a receipt and the audit name it.
const OpReadConfigMap = "configmap.get"

// ConfigMapReader is the one Kubernetes read a ConfigMap read makes.
type ConfigMapReader interface {
	GetConfigMap(ctx context.Context, namespace, name string) (*corev1.ConfigMap, error)
}

// ConfigMapRequest names one ConfigMap.
type ConfigMapRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ConfigMapKey is one key of a ConfigMap: its value through the output
// redaction, and the size of the value as stored.
type ConfigMapKey struct {
	Key   string `json:"key"`
	Bytes int    `json:"bytes"`
	Value string `json:"value"`
}

// ConfigMapReceipt is what one ConfigMap read returned. Values are always
// redacted - modules render connection strings and passwords into ConfigMaps
// in plain text - and the whole answer is bounded by the output budget;
// Truncated says a value or keys were cut.
type ConfigMapReceipt struct {
	Namespace         string         `json:"namespace"`
	Name              string         `json:"name"`
	UID               string         `json:"uid"`
	Keys              []ConfigMapKey `json:"keys"`
	BinaryKeys        []string       `json:"binary_keys,omitempty"`
	Truncated         bool           `json:"truncated"`
	RedactionContract string         `json:"redaction_contract"`
	At                time.Time      `json:"at"`
}

// credentialKey is a ConfigMap key whose value is a credential by its name -
// REDIS_PASSWORD, APP_SECRET, API_TOKEN - stored with nothing around it for
// the text redaction to recognize, so the whole value is masked.
var credentialKey = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|private[_-]?key|credential|access[_-]?key|auth)`)

// ReadConfigMap reads one ConfigMap in the namespace the request names, this
// process's own by default, every value through the output redaction, and a
// value under a credential-named key masked whole.
func (p *Provider) ReadConfigMap(ctx context.Context, request ConfigMapRequest) (ConfigMapReceipt, error) {
	result := ConfigMapReceipt{Namespace: request.Namespace, Name: request.Name, Keys: []ConfigMapKey{}, RedactionContract: RedactionContract, At: p.options.Now().UTC()}
	if p.options.ConfigMaps == nil {
		return result, &Error{Code: CodeAPIError, Message: "Kubernetes ConfigMap adapter is unavailable"}
	}
	namespace, err := p.namespace(request.Namespace)
	if err != nil {
		return result, err
	}
	result.Namespace = namespace
	if len(validation.IsDNS1123Subdomain(request.Name)) != 0 {
		return result, invalid("name must be a ConfigMap name")
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	configMap, err := p.options.ConfigMaps.GetConfigMap(ctx, namespace, request.Name)
	if err != nil {
		return result, apiError(err)
	}
	result.UID = string(configMap.UID)
	keys := make([]string, 0, len(configMap.Data))
	for key := range configMap.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > MaxSecretKeys {
		keys, result.Truncated = keys[:MaxSecretKeys], true
	}
	remaining := p.options.MaxOutputBytes
	for _, key := range keys {
		value := configMap.Data[key]
		masked := value
		if credentialKey.MatchString(key) && value != "" {
			masked = "<redacted>"
		}
		redacted, cut := p.redactor.bounded(masked, max(remaining, 0))
		remaining -= int64(len(redacted))
		result.Truncated = result.Truncated || cut
		result.Keys = append(result.Keys, ConfigMapKey{Key: key, Bytes: len(value), Value: redacted})
	}
	for key := range configMap.BinaryData {
		result.BinaryKeys = append(result.BinaryKeys, key)
	}
	sort.Strings(result.BinaryKeys)
	return result, nil
}

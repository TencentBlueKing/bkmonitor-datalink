// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"errors"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

// SREConfig is what a deployment may add to the SRE channel. Nothing here is
// required: the Pod provider reads any namespace a request names (this
// process's own by default) and the native query provider reaches the
// current UQ endpoint.
type SREConfig struct {
	// ModuleLabel is the label key a Pod's module is read from, written by
	// a parent chart whose modules carry their own key; the provider's
	// default key when empty.
	ModuleLabel string `yaml:"module_label,omitempty"`
	// Pods is accepted and ignored. Earlier builds took deployment-declared
	// Pod scopes here; the scope is now the request's, and a configuration
	// still carrying them must keep loading under strict decoding.
	Pods []map[string]any `yaml:"pods,omitempty"`
	UQ   SREUQConfig      `yaml:"uq,omitempty"`
}

type SREUQConfig struct {
	Egresses []uq.Egress `yaml:"egresses,omitempty"`
}

// DefaultSREEgress is the name of the egress synthesized for the current UQ
// endpoint when the deployment registers none.
const DefaultSREEgress = "current"

// DefaultSREEndpoints are the native QueryTs endpoints the synthesized egress
// carries, under the time contract registered egresses use.
var DefaultSREEndpoints = []uq.Endpoint{
	{Path: "/query/ts", StartField: "start_time", EndField: "end_time", TimeUnit: "s"},
	{Path: "/query/ts/raw", StartField: "start_time", EndField: "end_time", TimeUnit: "s"},
	{Path: "/query/ts/raw_with_scroll", StartField: "start_time", EndField: "end_time", TimeUnit: "s"},
}

// WithUQEndpoint resolves the egresses against the current runtime endpoint:
// a registered egress without a URL reuses it, and with none registered one
// egress reaching it is synthesized, any tenant and space the request
// names, as the detection queries reach it.
func (c SREConfig) WithUQEndpoint(endpoint string) SREConfig {
	c.UQ.Egresses = append([]uq.Egress(nil), c.UQ.Egresses...)
	if len(c.UQ.Egresses) == 0 && endpoint != "" {
		c.UQ.Egresses = []uq.Egress{{Name: DefaultSREEgress, URL: endpoint, Tenants: []string{"*"}, Spaces: []string{"*"},
			Endpoints: append([]uq.Endpoint(nil), DefaultSREEndpoints...)}}
		return c
	}
	for i := range c.UQ.Egresses {
		if c.UQ.Egresses[i].URL == "" {
			c.UQ.Egresses[i].URL = endpoint
		}
	}
	return c
}

func (c SREConfig) Validate() error {
	if c.ModuleLabel != "" && len(validation.IsQualifiedName(c.ModuleLabel)) != 0 {
		return errors.New("sre.module_label: a Kubernetes label key is required")
	}
	return uq.Validate(c.UQ.Egresses)
}

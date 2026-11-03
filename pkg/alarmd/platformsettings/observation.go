// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package platformsettings

import "time"

// Observation keeps the effective values and their publication provenance from
// one lock acquisition. Calling Current and Stats separately can pair two
// different refreshes. Raw source errors are deliberately not part of evidence.
type Observation struct {
	Settings Settings `json:"settings"`
	// Sources is the layer each field's effective value came from: DYNAMIC
	// (the platform's publication), VALUES (the deployment's own layer) or
	// DEFAULT (the platform's code default).
	Sources  map[Field]HorizonSource `json:"sources"`
	Mode     Mode                    `json:"mode"`
	Revision string                  `json:"revision"`
	LoadedAt time.Time               `json:"loaded_at"`
}

func (cache *Cache) Observe() Observation {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	sources := make(map[Field]HorizonSource, len(cache.sources))
	for field, source := range cache.sources {
		sources[field] = source
	}
	return Observation{Settings: cache.current, Sources: sources, Mode: cache.mode, Revision: cache.revision, LoadedAt: cache.loadedAt}
}

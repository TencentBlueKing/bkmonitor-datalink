// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package platformsettings

import "time"

// Observation keeps the effective values and their publication provenance from
// one lock acquisition. Calling Current and Stats separately can pair two
// different refreshes. Raw source errors are deliberately not part of evidence.
type Observation struct {
	Settings Settings  `json:"settings"`
	Mode     Mode      `json:"mode"`
	Revision string    `json:"revision"`
	LoadedAt time.Time `json:"loaded_at"`
}

func (cache *Cache) Observe() Observation {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return Observation{Settings: cache.current, Mode: cache.mode, Revision: cache.revision, LoadedAt: cache.loadedAt}
}

package metric

// RecordLegacyPodCache counts reads of the existing Python cache, never Pod or
// strategy identities. Batch-memo hits do not issue reads and are not counted.
func (r *Recorder) RecordLegacyPodCache(result string) {
	if r == nil {
		return
	}
	switch result {
	case "hit", "miss", "error":
		r.phaseTwo.legacyPodCache.WithLabelValues(result).Inc()
	}
}

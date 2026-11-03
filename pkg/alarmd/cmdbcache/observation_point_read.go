package cmdbcache

// ObservationDocumentKey reuses the dynamic-group writer contract for a
// diagnostic reader. The public request validates the semantic group ID.
func (reader *GroupReader) ObservationDocumentKey(id string) string { return reader.key(id) }

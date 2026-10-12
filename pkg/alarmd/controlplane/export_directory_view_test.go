// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

// RememberPublicationForTest has the reconciler hold catalog as the
// publication it just made, as a round that published it does.
func (reconciler *SourceReconciler) RememberPublicationForTest(publication SnapshotPublicationRef, catalog Catalog) {
	reconciler.rememberLastGood(publication, catalog)
}

// HoldsContentForTest says whether the repository's content memo - what the
// activation round reads the Slots' Plans from - answers publication.
func (repository *RedisCatalogRepository) HoldsContentForTest(publication SnapshotPublicationRef) bool {
	_, held := repository.contentMemo.lookup(publication)
	return held
}

// DecodedObjectBytesForTest is decodedObjectBytes.
func DecodedObjectBytesForTest(stored int) int { return decodedObjectBytes(stored) }

// ContentMemoForTest is the content memo alone, for a test of what it keeps.
type ContentMemoForTest struct{ memo publishedContentMemo }

// Store remembers content as a read of it does.
func (memo *ContentMemoForTest) Store(content PublishedContent) { memo.memo.store(content) }

// Release puts out the older content unless carried names it.
func (memo *ContentMemoForTest) Release(carried ...SnapshotPublicationRef) {
	memo.memo.release(func(publication SnapshotPublicationRef) bool {
		for _, named := range carried {
			if named == publication {
				return true
			}
		}
		return false
	})
}

// Holds says whether the memo answers publication.
func (memo *ContentMemoForTest) Holds(publication SnapshotPublicationRef) bool {
	_, held := memo.memo.lookup(publication)
	return held
}

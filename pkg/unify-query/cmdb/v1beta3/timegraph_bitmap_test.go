package v1beta3

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTimeBitmapOperationsAndOwnership(t *testing.T) {
	rng := rand.New(rand.NewSource(1499))
	for _, size := range []int{0, 1, 60, 64, 65, 128, 129, 257, 11001} {
		var left, right timeBitmap
		leftSet, rightSet := make(map[int]bool), make(map[int]bool)
		for i := 0; i < size; i++ {
			if rng.Intn(2) == 0 {
				left.set(i)
				leftSet[i] = true
			}
			if rng.Intn(2) == 0 {
				right.set(i)
				rightSet[i] = true
			}
		}
		union, intersection, difference := left.union(right), left.intersect(right), left.subtract(right)
		counts := [3]int{}
		for i := 0; i < size+65; i++ {
			want := [3]bool{leftSet[i] || rightSet[i], leftSet[i] && rightSet[i], leftSet[i] && !rightSet[i]}
			for j, bitmap := range []timeBitmap{union, intersection, difference} {
				require.Equal(t, want[j], bitmap.has(i), "size=%d operation=%d index=%d", size, j, i)
				if want[j] {
					counts[j]++
				}
			}
			require.Equal(t, leftSet[i], left.has(i))
			require.Equal(t, rightSet[i], right.has(i))
		}
		for j, bitmap := range []timeBitmap{union, intersection, difference} {
			require.Equal(t, counts[j], bitmap.count())
			require.Equal(t, counts[j] == 0, bitmap.empty())
			if size <= 64 {
				require.Nil(t, bitmap.rest)
			}
		}
	}
}

func TestTimeBitmapBoundaryBits(t *testing.T) {
	var bitmap timeBitmap
	for _, index := range []int{0, 63, 64, 127, 128, 255, 256} {
		bitmap.set(index)
	}
	require.Equal(t, 7, bitmap.count())
	for _, index := range []int{0, 63, 64, 127, 128, 255, 256} {
		require.True(t, bitmap.has(index))
	}
	for _, index := range []int{1, 62, 65, 126, 129, 254, 257} {
		require.False(t, bitmap.has(index))
	}
}

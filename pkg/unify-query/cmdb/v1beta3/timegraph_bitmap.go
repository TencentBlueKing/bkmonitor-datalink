// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import "math/bits"

// The topology grid contains at most 60 points, so its time set fits one word.
// Value operations leave both inputs unchanged.
type timeBitmap struct{ first uint64 }

func (b *timeBitmap) set(index int)     { b.first |= uint64(1) << index }
func (b timeBitmap) has(index int) bool { return b.first&(uint64(1)<<index) != 0 }
func (b timeBitmap) empty() bool        { return b.first == 0 }
func (b timeBitmap) count() int         { return bits.OnesCount64(b.first) }
func (b timeBitmap) union(other timeBitmap) timeBitmap {
	return timeBitmap{first: b.first | other.first}
}

func (b timeBitmap) intersect(other timeBitmap) timeBitmap {
	return timeBitmap{first: b.first & other.first}
}

func (b timeBitmap) subtract(other timeBitmap) timeBitmap {
	return timeBitmap{first: b.first &^ other.first}
}
func (b timeBitmap) isSubsetOf(other timeBitmap) bool { return b.first&^other.first == 0 }

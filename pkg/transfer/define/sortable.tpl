package define

import "github.com/cheekybits/genny/generic"

import "sort"

type ARRAY generic.Type
type ITEM generic.Type
type FIELD generic.Type

// ARRAY : ITEM array type
type ARRAY []ITEM

// Len : Len method for sort interface
func (a ARRAY) Len() int {
	return len(a)
}

// Less : Less method for sort interface
func (a ARRAY) Less(x, y int) bool {
	return a[x].FIELD < a[y].FIELD
}

// Swap : Swap method for sort interface
func (a ARRAY) Swap(x, y int) {
	v := a[x]
	a[x] = a[y]
	a[y] = v
}

// Sort : sort array directly
func (a ARRAY) Sort() {
	sort.Sort(a)
}
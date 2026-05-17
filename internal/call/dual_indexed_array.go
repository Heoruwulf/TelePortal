/*
TelePortal: High-performance, zero-allocation bi-directional audio bridge.
Copyright (C) 2026 Mark Horila

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/
package call

import (
	"sync"
)

// DualIndexedArray provides a highly concurrent, thread-safe collection.
// It stores pointers to a generic type T in a contiguous array for cache locality.
// It maintains two maps to provide O(1) lookups based on two distinct string keys.
// Removals are O(1) using the swap-and-pop technique.
type DualIndexedArray[T any] struct {
	index1 map[string]int
	index2 map[string]int
	key1Fn func(*T) string
	key2Fn func(*T) string
	items  []*T
	mu     sync.RWMutex
}

// NewDualIndexedArray creates a new array with the provided capacity and key extractors.
func NewDualIndexedArray[T any](capacity int, key1Fn, key2Fn func(*T) string) *DualIndexedArray[T] {
	return &DualIndexedArray[T]{
		items:  make([]*T, 0, capacity),
		index1: make(map[string]int, capacity),
		index2: make(map[string]int, capacity),
		key1Fn: key1Fn,
		key2Fn: key2Fn,
	}
}

// Add inserts an item. If an item with the same primary or secondary key
// already exists, it is overwritten.
func (d *DualIndexedArray[T]) Add(item *T) {
	d.mu.Lock()
	defer d.mu.Unlock()

	k1 := d.key1Fn(item)
	k2 := d.key2Fn(item)

	// Check if the item already exists by primary key
	if idx, exists := d.index1[k1]; exists {
		// Update existing
		oldItem := d.items[idx]
		oldK2 := d.key2Fn(oldItem)
		if oldK2 != k2 {
			delete(d.index2, oldK2)
			d.index2[k2] = idx
		}
		d.items[idx] = item
		return
	}

	// Add new item
	idx := len(d.items)
	d.items = append(d.items, item)
	d.index1[k1] = idx
	d.index2[k2] = idx
}

// GetByKey1 retrieves an item by its primary key.
func (d *DualIndexedArray[T]) GetByKey1(k1 string) (*T, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	idx, ok := d.index1[k1]
	if !ok {
		return nil, false
	}
	return d.items[idx], true
}

// GetByKey2 retrieves an item by its secondary key.
func (d *DualIndexedArray[T]) GetByKey2(k2 string) (*T, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	idx, ok := d.index2[k2]
	if !ok {
		return nil, false
	}
	return d.items[idx], true
}

// RemoveByKey1 deletes an item using its primary key.
func (d *DualIndexedArray[T]) RemoveByKey1(k1 string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	idx, ok := d.index1[k1]
	if !ok {
		return
	}
	d.removeAt(idx)
}

// RemoveByKey2 deletes an item using its secondary key.
func (d *DualIndexedArray[T]) RemoveByKey2(k2 string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	idx, ok := d.index2[k2]
	if !ok {
		return
	}
	d.removeAt(idx)
}

// removeAt performs the O(1) swap-and-pop. Assumes lock is held.
func (d *DualIndexedArray[T]) removeAt(idx int) {
	item := d.items[idx]
	k1 := d.key1Fn(item)
	k2 := d.key2Fn(item)

	delete(d.index1, k1)
	delete(d.index2, k2)

	lastIdx := len(d.items) - 1
	if idx != lastIdx {
		// Swap with the last element
		lastItem := d.items[lastIdx]
		d.items[idx] = lastItem

		// Update indices for the moved item
		lastK1 := d.key1Fn(lastItem)
		lastK2 := d.key2Fn(lastItem)
		d.index1[lastK1] = idx
		d.index2[lastK2] = idx
	}

	// Pop the last element to prevent memory leak and shrink slice
	d.items[lastIdx] = nil
	d.items = d.items[:lastIdx]
}

// Len returns the number of items.
func (d *DualIndexedArray[T]) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.items)
}

// Values returns a copy of the slice containing all items.
func (d *DualIndexedArray[T]) Values() []*T {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]*T, len(d.items))
	copy(out, d.items)
	return out
}

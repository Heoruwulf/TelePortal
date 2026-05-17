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
	"strconv"
	"sync"
	"testing"
)

type testItem struct {
	ID1 string
	ID2 string
	Val string
}

func TestDualIndexedArray(t *testing.T) {
	t.Parallel()

	k1Fn := func(i *testItem) string { return i.ID1 }
	k2Fn := func(i *testItem) string { return i.ID2 }

	t.Run("AddAndGet", func(t *testing.T) {
		t.Parallel()
		d := NewDualIndexedArray(10, k1Fn, k2Fn)

		item := &testItem{"a1", "b1", "val1"}
		d.Add(item)

		if d.Len() != 1 {
			t.Errorf("expected len 1, got %d", d.Len())
		}

		got1, ok := d.GetByKey1("a1")
		if !ok || got1.Val != "val1" {
			t.Errorf("GetByKey1 failed")
		}

		got2, ok := d.GetByKey2("b1")
		if !ok || got2.Val != "val1" {
			t.Errorf("GetByKey2 failed")
		}
	})

	t.Run("RemoveAndSwap", func(t *testing.T) {
		t.Parallel()
		d := NewDualIndexedArray(10, k1Fn, k2Fn)

		item1 := &testItem{"a1", "b1", "val1"}
		item2 := &testItem{"a2", "b2", "val2"}
		item3 := &testItem{"a3", "b3", "val3"}

		d.Add(item1)
		d.Add(item2)
		d.Add(item3)

		// Remove middle item to trigger swap with last item
		d.RemoveByKey1("a2")

		if d.Len() != 2 {
			t.Errorf("expected len 2, got %d", d.Len())
		}

		// Ensure removed item is gone
		if _, ok := d.GetByKey1("a2"); ok {
			t.Errorf("expected a2 to be removed")
		}
		if _, ok := d.GetByKey2("b2"); ok {
			t.Errorf("expected b2 to be removed")
		}

		// Ensure swapped item (a3) is still accessible
		got3, ok := d.GetByKey1("a3")
		if !ok || got3.Val != "val3" {
			t.Errorf("expected a3 to be accessible after swap")
		}
		got3b, ok := d.GetByKey2("b3")
		if !ok || got3b.Val != "val3" {
			t.Errorf("expected b3 to be accessible after swap")
		}

		// Internal slice structure check (a3 should now be at index 1)
		if d.items[1].ID1 != "a3" {
			t.Errorf("expected a3 to be swapped to index 1, got %s", d.items[1].ID1)
		}
	})

	t.Run("UpdateExisting", func(t *testing.T) {
		t.Parallel()
		d := NewDualIndexedArray(10, k1Fn, k2Fn)

		item1 := &testItem{"a1", "b1", "val1"}
		d.Add(item1)

		// Update same k1, different k2
		item2 := &testItem{"a1", "b2", "val2"}
		d.Add(item2)

		if d.Len() != 1 {
			t.Errorf("expected len 1, got %d", d.Len())
		}

		// Old k2 should be gone
		if _, ok := d.GetByKey2("b1"); ok {
			t.Errorf("expected b1 to be removed after update")
		}

		// New k2 should exist
		got, ok := d.GetByKey2("b2")
		if !ok || got.Val != "val2" {
			t.Errorf("expected b2 to be updated")
		}
	})

	t.Run("Concurrency", func(t *testing.T) {
		t.Parallel()
		d := NewDualIndexedArray(100, k1Fn, k2Fn)
		var wg sync.WaitGroup

		workers := 50
		itemsPerWorker := 100

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < itemsPerWorker; j++ {
					id := strconv.Itoa(workerID*itemsPerWorker + j)
					d.Add(&testItem{id, "b" + id, "val"})
				}
			}(i)
		}

		wg.Wait()

		expectedLen := workers * itemsPerWorker
		if d.Len() != expectedLen {
			t.Errorf("expected len %d, got %d", expectedLen, d.Len())
		}

		// Test concurrent reads and removes
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < itemsPerWorker; j++ {
					id := strconv.Itoa(workerID*itemsPerWorker + j)
					if j%2 == 0 {
						d.GetByKey1(id)
						d.GetByKey2("b" + id)
					} else {
						d.RemoveByKey1(id)
					}
				}
			}(i)
		}

		wg.Wait()
	})
}

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
package audio

import (
	"sync"
)

const (
	// Size160 is for 8kHz G.711 (160 bytes)
	Size160 = 160
	// Size320 is for 8kHz L16 (320 bytes) or 16kHz G.711
	Size320 = 320
	// Size640 is for 16kHz L16 (640 bytes)
	Size640 = 640
	// Size960 is for 24kHz L16 (960 bytes)
	Size960 = 960
	// Size1920 is for interleaved 24kHz L16
	Size1920 = 1920
	// SizeDefault is the default buffer size (2048 bytes)
	SizeDefault = 2048
	// SizeRTPThreshold is used to catch buffers that had RTP headers (12 bytes) sliced off
	SizeRTPThreshold = 2000
)

// Global pools for common audio buffer sizes to reduce GC pressure.
// We use a few standard sizes corresponding to common ptime (20ms) and sample rates.
var (
	// pool160 is for 8kHz G.711 (160 bytes)
	pool160 = sync.Pool{
		New: func() any {
			return make([]byte, Size160)
		},
	}
	// pool320 is for 8kHz L16 (320 bytes) or 16kHz G.711
	pool320 = sync.Pool{
		New: func() any {
			return make([]byte, Size320)
		},
	}
	// pool640 is for 16kHz L16 (640 bytes)
	pool640 = sync.Pool{
		New: func() any {
			return make([]byte, Size640)
		},
	}
	// pool960 is for 24kHz L16 (960 bytes)
	pool960 = sync.Pool{
		New: func() any {
			return make([]byte, Size960)
		},
	}
	// poolDefault is for arbitrary sizes
	poolDefault = sync.Pool{
		New: func() any {
			return make([]byte, SizeDefault)
		},
	}

	// Pools for []int slices (used for recording and processing)
	intPool160 = sync.Pool{
		New: func() any {
			return make([]int, Size160)
		},
	}
	intPool320 = sync.Pool{
		New: func() any {
			return make([]int, Size320)
		},
	}
	intPool640 = sync.Pool{
		New: func() any {
			return make([]int, Size640)
		},
	}
	intPool960 = sync.Pool{
		New: func() any {
			return make([]int, Size960)
		},
	}
	intPool1920 = sync.Pool{
		New: func() any {
			return make([]int, Size1920) // Interleaved 960
		},
	}
	intPoolDefault = sync.Pool{
		New: func() any {
			return make([]int, SizeDefault)
		},
	}
)

// GetBuffer retrieves a byte slice of at least the requested size from a pool.
func GetBuffer(size int) []byte {
	var b []byte
	switch {
	case size <= Size160:
		b = pool160.Get().([]byte)
	case size <= Size320:
		b = pool320.Get().([]byte)
	case size <= Size640:
		b = pool640.Get().([]byte)
	case size <= Size960:
		b = pool960.Get().([]byte)
	default:
		b = poolDefault.Get().([]byte)
	}

	// For the default pool, we often get back slices that are slightly smaller than the full capacity
	// due to slicing (e.g. 4084 if 12 byte RTP header was removed).
	if cap(b) < size {
		return make([]byte, size)
	}
	return b[:size]
}

// PutBuffer returns a byte slice to the appropriate pool.
//
//lint:ignore SA6002 Profiling confirmed passing slice value is faster than pointer dereferencing here
func PutBuffer(b []byte) {
	if cap(b) == 0 {
		return
	}
	// Restore the slice to its full capacity before returning to the pool.
	// This ensures that the next consumer gets a buffer with the maximum possible capacity.
	b = b[:cap(b)]
	size := len(b)

	switch {
	case size >= SizeRTPThreshold: // Catch buffers that had RTP headers (12 bytes) sliced off
		poolDefault.Put(b)
	case size >= Size960:
		pool960.Put(b)
	case size >= Size640:
		pool640.Put(b)
	case size >= Size320:
		pool320.Put(b)
	default:
		pool160.Put(b)
	}
}

// GetIntBuffer retrieves an int slice of at least the requested size from a pool.
func GetIntBuffer(size int) []int {
	var b []int
	switch {
	case size <= Size160:
		b = intPool160.Get().([]int)
	case size <= Size320:
		b = intPool320.Get().([]int)
	case size <= Size640:
		b = intPool640.Get().([]int)
	case size <= Size960:
		b = intPool960.Get().([]int)
	case size <= Size1920:
		b = intPool1920.Get().([]int)
	default:
		b = intPoolDefault.Get().([]int)
	}

	if cap(b) < size {
		return make([]int, size)
	}
	return b[:size]
}

// PutIntBuffer returns an int slice to the appropriate pool.
//
//lint:ignore SA6002 Profiling confirmed passing slice value is faster than pointer dereferencing here
func PutIntBuffer(b []int) {
	if cap(b) == 0 {
		return
	}
	// Restore the slice to its full capacity before returning to the pool.
	b = b[:cap(b)]
	size := len(b)

	switch {
	case size >= SizeRTPThreshold: // Hardening: Use same threshold for int buffers
		intPoolDefault.Put(b)
	case size >= Size1920:
		intPool1920.Put(b)
	case size >= Size960:
		intPool960.Put(b)
	case size >= Size640:
		intPool640.Put(b)
	case size >= Size320:
		intPool320.Put(b)
	default:
		intPool160.Put(b)
	}
}

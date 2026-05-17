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
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math/rand"
)

const (
	// Buffer configuration
	defaultBufferCapacity = 4096
	maxOggPageSize        = 65025
	maxSegmentSize        = 255

	// Ogg Protocol constants
	oggCapturePattern  = "OggS"
	oggVersion         = 0
	oggHeaderFixedSize = 27
	oggCrcPolynomial   = 0x04c11db7

	// Ogg Header Offsets
	oggOffsetVersion      = 4
	oggOffsetHeaderType   = 5
	oggOffsetGranulePos   = 6
	oggOffsetSerial       = 14
	oggOffsetPageSequence = 18
	oggOffsetChecksum     = 22
	oggOffsetSegments     = 26
	oggOffsetSegmentTable = 27

	// Ogg Page Flags
	oggFlagBOS = 0x02

	// Opus Head Constants
	opusHeadSignature     = "OpusHead"
	opusHeadVersion       = 1
	opusHeadSize          = 19
	opusHeadPreSkip       = 3840
	opusHeadOutputGain    = 0
	opusHeadMappingFamily = 0

	// Opus Head Offsets
	opusHeadOffsetVersion       = 8
	opusHeadOffsetChannels      = 9
	opusHeadOffsetPreSkip       = 10
	opusHeadOffsetSampleRate    = 12
	opusHeadOffsetOutputGain    = 16
	opusHeadOffsetMappingFamily = 18

	// Opus Tags Constants
	opusTagsSignature       = "OpusTags"
	opusTagsVendor          = "ConvoIntel"
	opusTagsUserCommentsLen = 0

	// Opus Tags Offsets and Sizes
	opusTagsOffsetVendorLen = 8
	opusTagsOffsetVendorStr = 12
	sizeUint32              = 4
)

// OggWriter handles encapsulation of Opus packets into an Ogg stream.
type OggWriter struct {
	// --- Pointer-containing fields (GC scan prefix) ---

	// 16 bytes
	output io.Writer

	// 8 bytes
	buffer     *bytes.Buffer
	pageBuffer []byte // Buffer for accumulating packets into a page
	segments   []byte // Segment table

	// --- Scalar / Non-pointer fields ---

	// 8 bytes
	granulePos  uint64
	packetCount uint64

	// 4 bytes
	serial       uint32
	pageSequence uint32

	// 1 byte
	headerWritten bool
}

// NewOggWriter creates a new OggWriter.
func NewOggWriter(output io.Writer) *OggWriter {
	// Initialize with a random serial number
	serial := rand.Uint32()

	return &OggWriter{
		serial:     serial,
		output:     output,
		buffer:     bytes.NewBuffer(make([]byte, 0, defaultBufferCapacity)),
		pageBuffer: make([]byte, 0, maxOggPageSize), // Max Ogg page size is roughly 64KB
		segments:   make([]byte, 0, maxSegmentSize),
	}
}

// WriteOpusHeader writes the initial Ogg Opus headers (ID Header and Comment Header).
// This is required at the start of the stream.
// sampleRate: The input sample rate (e.g. 48000).
// channels: Number of channels (e.g. 1 or 2).
func (w *OggWriter) WriteOpusHeader(sampleRate int, channels int) error {
	if w.headerWritten {
		return nil
	}

	// 1. Identification Header
	// Magic Signature "OpusHead" (8 bytes)
	// Version (1 byte) = 1
	// Channel Count (1 byte)
	// Pre-skip (2 bytes) = 3840 (80ms at 48kHz, recommended default)
	// Input Sample Rate (4 bytes)
	// Output Gain (2 bytes) = 0
	// Channel Mapping Family (1 byte) = 0 (Mono/Stereo)
	idHeader := make([]byte, opusHeadSize)
	copy(idHeader[0:], []byte(opusHeadSignature))
	idHeader[opusHeadOffsetVersion] = opusHeadVersion
	idHeader[opusHeadOffsetChannels] = uint8(channels)
	binary.LittleEndian.PutUint16(idHeader[opusHeadOffsetPreSkip:], opusHeadPreSkip) // Pre-skip
	binary.LittleEndian.PutUint32(idHeader[opusHeadOffsetSampleRate:], uint32(sampleRate))
	binary.LittleEndian.PutUint16(idHeader[opusHeadOffsetOutputGain:], opusHeadOutputGain) // Output Gain
	idHeader[opusHeadOffsetMappingFamily] = opusHeadMappingFamily                          // Mapping Family

	if err := w.writePage([]byte{oggFlagBOS}, [][]byte{idHeader}); err != nil { // BOS flag
		return err
	}

	// 2. Comment Header
	// Magic Signature "OpusTags" (8 bytes)
	// Vendor String Length (4 bytes)
	// Vendor String
	// User Comment List Length (4 bytes)
	// ... comments
	vendor := opusTagsVendor
	commentHeader := make([]byte, len(opusTagsSignature)+sizeUint32+len(vendor)+sizeUint32)
	copy(commentHeader[0:], []byte(opusTagsSignature))
	binary.LittleEndian.PutUint32(commentHeader[opusTagsOffsetVendorLen:], uint32(len(vendor)))
	copy(commentHeader[opusTagsOffsetVendorStr:], []byte(vendor))
	binary.LittleEndian.PutUint32(commentHeader[opusTagsOffsetVendorStr+len(vendor):], opusTagsUserCommentsLen) // No user comments

	if err := w.writePage([]byte{}, [][]byte{commentHeader}); err != nil {
		return err
	}

	w.headerWritten = true
	return nil
}

// WritePacket writes a raw Opus packet to the Ogg stream.
// It frames the packet and flushes a page if necessary.
// samples: The number of PCM samples in this packet (e.g. 960 for 20ms at 48kHz).
func (w *OggWriter) WritePacket(packet []byte, samples uint32) error {
	// Simple implementation: One packet per page for low latency streaming.
	// For file storage, we'd bundle multiple packets.

	w.granulePos += uint64(samples)
	w.packetCount++

	return w.writePage([]byte{}, [][]byte{packet})
}

// writePage constructs and writes an Ogg page with the given packets.
// flags: 0x02 = BOS (Beginning of Stream), 0x04 = EOS (End of Stream), 0x01 = Continuation
func (w *OggWriter) writePage(flags []byte, packets [][]byte) error {
	// Header Type (Flags)
	var headerType byte
	if len(flags) > 0 {
		headerType = flags[0]
	}

	// Calculate segment table
	segments := make([]byte, 0)
	payloadLen := 0
	for _, p := range packets {
		l := len(p)
		payloadLen += l
		for l >= maxSegmentSize {
			segments = append(segments, maxSegmentSize)
			l -= maxSegmentSize
		}
		segments = append(segments, byte(l))
	}

	// Ogg Header (27 bytes + segment table size)
	header := make([]byte, oggHeaderFixedSize+len(segments))
	copy(header[0:], []byte(oggCapturePattern)) // Capture Pattern
	header[oggOffsetVersion] = oggVersion       // Version
	header[oggOffsetHeaderType] = headerType
	binary.LittleEndian.PutUint64(header[oggOffsetGranulePos:], w.granulePos)
	binary.LittleEndian.PutUint32(header[oggOffsetSerial:], w.serial)
	binary.LittleEndian.PutUint32(header[oggOffsetPageSequence:], w.pageSequence)
	// Checksum at 22 (4 bytes), initialized to 0
	header[oggOffsetSegments] = byte(len(segments)) // Page Segments count
	copy(header[oggOffsetSegmentTable:], segments)

	// Calculate CRC32
	// Checksum covers Header + Payload
	crc := crc32.New(crc32.MakeTable(oggCrcPolynomial)) // Ogg uses standard polynomial
	crc.Write(header)
	for _, p := range packets {
		crc.Write(p)
	}
	sum := crc.Sum32()
	binary.LittleEndian.PutUint32(header[oggOffsetChecksum:], sum)

	// Write to output
	if _, err := w.output.Write(header); err != nil {
		return err
	}
	for _, p := range packets {
		if _, err := w.output.Write(p); err != nil {
			return err
		}
	}

	w.pageSequence++
	return nil
}

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
	"encoding/binary"
	"fmt"

	audiopool "github.com/heoruwulf/teleportal/pkg/audio"
)

type CodecName string

const (
	CodecPCMU CodecName = "PCMU" // G.711 μ-law
	CodecPCMA CodecName = "PCMA" // G.711 A-law
	CodecL16  CodecName = "L16"  // PCM 16-bit
	CodecOpus CodecName = "opus" // Opus
	CodecPass CodecName = "PASS" // Pass-through mode

	PayloadTypePCMU uint8 = 0
	PayloadTypePCMA uint8 = 8
	PayloadTypeL16  uint8 = 96  // Dynamic range
	PayloadTypeOpus uint8 = 111 // Common dynamic range for Opus
)

// Codec represents an audio codec with its properties.
// Note: Fields are ordered by size (largest to smallest) to minimize memory padding.
type Codec struct {
	// 16 bytes
	Name CodecName `json:"name"`

	// 8 bytes
	BytesPerSample int `json:"bytes_per_sample"`
	SampleRate     int `json:"sample_rate"`
	Channels       int `json:"channels"`

	// 1 byte
	PayloadType uint8 `json:"payload_type"`
	IsBigEndian bool  `json:"is_big_endian"`
	IsSigned    bool  `json:"is_signed"`
}

type Stream struct {
	// 48 bytes
	Codec Codec `json:"codec"`

	// 8 bytes (packetization time in ms)
	PTime int `json:"ptime"`

	// 1 byte
	DTMFPayloadType uint8 `json:"dtmf_payload_type"`
}

func (s Stream) PacketTimeInBytes() int {
	// For Opus, this might not be constant as it is a VBR codec,
	// but for fixed-buffer allocations we can estimate or use it as a hint.
	// However, SIP negotiation uses it for the PCM side usually.
	// Calculate total samples first to avoid integer division truncation (e.g., 44100 / 1000 = 44)
	totalSamples := (s.Codec.SampleRate * s.PTime) / 1000
	return totalSamples * s.Codec.BytesPerSample * s.Codec.Channels
}

func (s Stream) SamplesPerPacket() uint32 {
	// Calculate total samples first to avoid integer division truncation
	return uint32((s.Codec.SampleRate * s.PTime) / 1000)
}

func (c Codec) Bits() int {
	return c.BytesPerSample * 8
}

// Equals checks if two Codec instances are equal.
func (c Codec) Equals(other Codec) bool {
	return c.Name == other.Name &&
		c.IsBigEndian == other.IsBigEndian &&
		c.IsSigned == other.IsSigned &&
		c.BytesPerSample == other.BytesPerSample &&
		c.SampleRate == other.SampleRate &&
		c.Channels == other.Channels
}

func IsValidSampleRate(rate int) bool {
	switch rate {
	case 8000, 16000, 24000, 32000, 44100, 48000:
		return true
	default:
		return false
	}
}

func IsValidChannels(channels int) bool {
	return channels == 1 || channels == 2
}

func NewCodecG711MuLaw() (Codec, error) {
	return Codec{
		PayloadType:    PayloadTypePCMU,
		Name:           CodecPCMU,
		IsBigEndian:    false,
		IsSigned:       false,
		BytesPerSample: 1,
		SampleRate:     8000,
		Channels:       1,
	}, nil
}

func NewCodecG711ALaw() (Codec, error) {
	return Codec{
		PayloadType:    PayloadTypePCMA,
		Name:           CodecPCMA,
		IsBigEndian:    false,
		IsSigned:       false,
		BytesPerSample: 1,
		SampleRate:     8000,
		Channels:       1,
	}, nil
}

func NewCodecOpus(payloadType uint8, sampleRate, channels int) (Codec, error) {
	if !IsValidSampleRate(sampleRate) {
		return Codec{}, fmt.Errorf("invalid sample rate %d for Opus", sampleRate)
	}
	if !IsValidChannels(channels) {
		return Codec{}, fmt.Errorf("invalid channels %d for Opus", channels)
	}
	return Codec{
		PayloadType:    payloadType,
		Name:           CodecOpus,
		IsBigEndian:    false,
		IsSigned:       true,
		BytesPerSample: 2, // After decoding to L16
		SampleRate:     sampleRate,
		Channels:       channels,
	}, nil
}

func NewCodecL16(payloadType uint8, sampleRate int, channels int, isBigEndian, isSigned bool) (Codec, error) {
	if !IsValidSampleRate(sampleRate) {
		return Codec{}, fmt.Errorf("invalid sample rate %d for L16", sampleRate)
	}
	if !IsValidChannels(channels) {
		return Codec{}, fmt.Errorf("invalid channels %d for L16", channels)
	}
	return Codec{
		PayloadType:    payloadType,
		Name:           CodecL16,
		IsBigEndian:    isBigEndian,
		IsSigned:       isSigned,
		BytesPerSample: 2,
		SampleRate:     sampleRate,
		Channels:       channels,
	}, nil
}

// DecodePCMUToL16 converts a slice of 8-bit PCMU (G.711 μ-law) audio samples
// into a slice of 16-bit signed little-endian PCM samples (L16).
func DecodePCMUToL16(pcmu []byte) ([]byte, error) {
	if pcmu == nil {
		return nil, fmt.Errorf("input pcmu slice cannot be nil")
	}

	// Each 8-bit sample becomes a 16-bit (2-byte) sample.
	l16 := audiopool.GetBuffer(len(pcmu) * 2)

	for i, sample := range pcmu {
		// Look up the 16-bit PCM value from the table.
		pcmSample := muLawToPcmTable[sample]

		// Write the 16-bit value as two 8-bit bytes (little-endian).
		// The Web Audio API typically works best with little-endian PCM.
		binary.LittleEndian.PutUint16(l16[i*2:], uint16(pcmSample))
	}

	return l16, nil
}

// DecodePCMAToL16 converts a slice of 8-bit PCMA (G.711 A-law) audio samples
// into a slice of 16-bit signed little-endian PCM samples (L16).
func DecodePCMAToL16(pcma []byte) ([]byte, error) {
	if pcma == nil {
		return nil, fmt.Errorf("input pcma slice cannot be nil")
	}

	l16 := audiopool.GetBuffer(len(pcma) * 2)

	for i, sample := range pcma {
		pcmSample := aLawToPcmTable[sample]
		binary.LittleEndian.PutUint16(l16[i*2:], uint16(pcmSample))
	}

	return l16, nil
}

// DecodeL16BEToL16LE converts a slice of 16-bit Big-Endian PCM samples (standard RTP L16)
// into a slice of 16-bit signed Little-Endian PCM samples (standard internal format).
func DecodeL16BEToL16LE(input []byte) ([]byte, error) {
	if input == nil {
		return nil, fmt.Errorf("input l16 slice cannot be nil")
	}
	if len(input)%2 != 0 {
		return nil, fmt.Errorf("input l16 slice length must be even")
	}

	// We can allocate a new slice or modify in place.
	// For safety in concurrency/buffering, we use a pooled slice.
	output := audiopool.GetBuffer(len(input))

	for i := 0; i < len(input); i += 2 {
		// Swap bytes: BE [High, Low] -> LE [Low, High]
		output[i] = input[i+1]
		output[i+1] = input[i]
	}

	return output, nil
}

// DecodeL16LEToL16BE converts a slice of 16-bit Little-Endian PCM samples
// into a slice of 16-bit signed Big-Endian PCM samples (standard RTP L16).
func DecodeL16LEToL16BE(input []byte) ([]byte, error) {
	return DecodeL16BEToL16LE(input)
}

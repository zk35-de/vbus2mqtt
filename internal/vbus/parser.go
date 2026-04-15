// Package vbus implements a minimal RESOL VBus protocol v1 parser.
//
// Protocol reference:
//   https://danielwippermann.github.io/resol-vbus/#/md/docs/vbus-specification
//
// Frame format (protocol version 0x10):
//
//	SYNC(1) DST(2LE) SRC(2LE) VER(1=0x10) CMD(2LE) NFRAMES(1) HDRCS(1)
//	  [BYTE0 BYTE1 BYTE2 BYTE3 SEPTETT FRAMECS] × NFRAMES
//
// Every transmitted byte has bit 7 cleared; the SEPTETT byte carries the
// original bit-7 values of the four preceding data bytes.
// Checksum: (0x7F - sum(covered_bytes)) & 0x7F
package vbus

import (
	"encoding/binary"
	"fmt"
	"log/slog"
)

const syncByte byte = 0xAA

// Frame is a fully decoded VBus protocol-v1 data telegram.
type Frame struct {
	Destination uint16
	Source      uint16
	Command     uint16
	// Payload contains the concatenated, bit-7-restored data bytes of all
	// sub-frames (4 bytes per sub-frame × NFRAMES).
	Payload []byte
}

// Parser accumulates raw bytes and emits complete Frames.
// It is not safe for concurrent use.
type Parser struct {
	buf []byte
	log *slog.Logger
}

func NewParser(log *slog.Logger) *Parser {
	return &Parser{
		buf: make([]byte, 0, 2048),
		log: log,
	}
}

// Feed appends raw bytes and returns any fully parsed frames.
func (p *Parser) Feed(data []byte) []Frame {
	p.buf = append(p.buf, data...)
	return p.drain()
}

// drain processes the internal buffer, extracting complete frames.
func (p *Parser) drain() []Frame {
	var out []Frame
	buf := p.buf

	for {
		// ── locate SYNC ────────────────────────────────────────────────────
		if len(buf) == 0 || buf[0] != syncByte {
			idx := -1
			for i, b := range buf {
				if b == syncByte {
					idx = i
					break
				}
			}
			if idx < 0 {
				buf = buf[:0]
				break
			}
			buf = buf[idx:]
		}

		// ── need full header (10 bytes) ────────────────────────────────────
		if len(buf) < 10 {
			break
		}

		ver := buf[5]
		if ver != 0x10 {
			// Not a v1 frame; skip this SYNC and keep scanning.
			buf = buf[1:]
			continue
		}

		dst     := binary.LittleEndian.Uint16(buf[1:3])
		src     := binary.LittleEndian.Uint16(buf[3:5])
		cmd     := binary.LittleEndian.Uint16(buf[6:8])
		nFrames := int(buf[8])
		hdrCS   := buf[9]

		if want := checksum(buf[1:9]); hdrCS != want {
			p.log.Debug("vbus: header checksum mismatch",
				"got", fmt.Sprintf("%02X", hdrCS),
				"want", fmt.Sprintf("%02X", want),
				"src", fmt.Sprintf("0x%04X", src),
			)
			buf = buf[1:]
			continue
		}

		totalLen := 10 + nFrames*6
		if len(buf) < totalLen {
			break // incomplete, wait for more bytes
		}

		payload, ok := decodeSubFrames(buf[10:10+nFrames*6], nFrames)
		if !ok {
			p.log.Debug("vbus: sub-frame checksum mismatch, skipping",
				"src", fmt.Sprintf("0x%04X", src))
			buf = buf[1:]
			continue
		}

		out = append(out, Frame{
			Destination: dst,
			Source:      src,
			Command:     cmd,
			Payload:     payload,
		})

		p.log.Debug("vbus: frame decoded",
			"src", fmt.Sprintf("0x%04X", src),
			"dst", fmt.Sprintf("0x%04X", dst),
			"cmd", fmt.Sprintf("0x%04X", cmd),
			"payload_bytes", len(payload),
		)

		buf = buf[totalLen:]
	}

	p.buf = buf
	return out
}

// decodeSubFrames decodes nFrames×6-byte sub-frame blocks into payload bytes.
func decodeSubFrames(data []byte, nFrames int) ([]byte, bool) {
	payload := make([]byte, 0, nFrames*4)
	for i := range nFrames {
		base    := i * 6
		raw     := data[base : base+4]
		septett := data[base+4]
		cs      := data[base+5]

		if want := checksum(data[base : base+5]); cs != want {
			return nil, false
		}

		var decoded [4]byte
		copy(decoded[:], raw)
		for j := range 4 {
			if septett&(1<<uint(j)) != 0 {
				decoded[j] |= 0x80
			}
		}
		payload = append(payload, decoded[:]...)
	}
	return payload, true
}

// checksum computes the VBus checksum: (0x7F - Σbytes) & 0x7F
func checksum(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	return (0x7F - sum) & 0x7F
}

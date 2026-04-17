package vbus

import (
	"log/slog"
	"testing"
)

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nil_writer{}, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

type nil_writer struct{}

func (nil_writer) Write(p []byte) (int, error) { return len(p), nil }

// buildFrame constructs a valid raw VBus v1 frame from parts.
// nFrames payload bytes must already be in decoded form; this function
// encodes them into sub-frames with correct septett and checksums.
func buildFrame(dst, src, cmd uint16, payloadBytes []byte) []byte {
	nFrames := (len(payloadBytes) + 3) / 4

	header := []byte{
		0xAA,
		byte(dst), byte(dst >> 8),
		byte(src), byte(src >> 8),
		0x10,
		byte(cmd), byte(cmd >> 8),
		byte(nFrames),
	}
	header = append(header, checksum(header[1:]))

	var data []byte
	for i := range nFrames {
		var raw [4]byte
		for j := range 4 {
			idx := i*4 + j
			if idx < len(payloadBytes) {
				raw[j] = payloadBytes[idx]
			}
		}
		var septett byte
		for j := range 4 {
			if raw[j]&0x80 != 0 {
				septett |= 1 << uint(j)
				raw[j] &^= 0x80
			}
		}
		sf := []byte{raw[0], raw[1], raw[2], raw[3], septett}
		sf = append(sf, checksum(sf))
		data = append(data, sf...)
	}

	return append(header, data...)
}

func TestParser_SingleFrame(t *testing.T) {
	p := NewParser(nopLogger())
	payload := make([]byte, 4)
	raw := buildFrame(0x0010, 0x7112, 0x0100, payload)

	frames := p.Feed(raw)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	f := frames[0]
	if f.Source != 0x7112 {
		t.Errorf("Source: got 0x%04X, want 0x7112", f.Source)
	}
	if f.Destination != 0x0010 {
		t.Errorf("Destination: got 0x%04X, want 0x0010", f.Destination)
	}
	if f.Command != 0x0100 {
		t.Errorf("Command: got 0x%04X, want 0x0100", f.Command)
	}
}

func TestParser_GarbageBeforeFrame(t *testing.T) {
	p := NewParser(nopLogger())
	payload := make([]byte, 4)
	raw := buildFrame(0x0010, 0x7112, 0x0100, payload)
	garbage := []byte{0x01, 0x02, 0x55, 0xFF}
	frames := p.Feed(append(garbage, raw...))
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame after garbage, got %d", len(frames))
	}
}

func TestParser_TwoConsecutiveFrames(t *testing.T) {
	p := NewParser(nopLogger())
	payload := make([]byte, 4)
	raw1 := buildFrame(0x0010, 0x7112, 0x0100, payload)
	raw2 := buildFrame(0x0010, 0x7110, 0x0100, payload)
	frames := p.Feed(append(raw1, raw2...))
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	if frames[0].Source != 0x7112 || frames[1].Source != 0x7110 {
		t.Error("wrong source addresses")
	}
}

func TestParser_SplitFeed(t *testing.T) {
	p := NewParser(nopLogger())
	payload := make([]byte, 4)
	raw := buildFrame(0x0010, 0x7112, 0x0100, payload)

	// Feed half, then the rest – should still produce one frame.
	mid := len(raw) / 2
	if got := p.Feed(raw[:mid]); len(got) != 0 {
		t.Fatal("expected no frames from partial data")
	}
	frames := p.Feed(raw[mid:])
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame after second half, got %d", len(frames))
	}
}

func TestParser_BadChecksum(t *testing.T) {
	p := NewParser(nopLogger())
	payload := make([]byte, 4)
	raw := buildFrame(0x0010, 0x7112, 0x0100, payload)
	raw[9] ^= 0xFF // corrupt header checksum
	frames := p.Feed(raw)
	if len(frames) != 0 {
		t.Fatal("expected no frames for corrupt checksum")
	}
}

func TestParser_HighBitPayload(t *testing.T) {
	p := NewParser(nopLogger())
	// Payload with bit-7 set – must survive septett encode/decode round-trip.
	payload := []byte{0x80, 0xAB, 0x00, 0xCD}
	raw := buildFrame(0x0010, 0x7112, 0x0100, payload)
	frames := p.Feed(raw)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if frames[0].Payload[0] != 0x80 {
		t.Errorf("payload[0]: got 0x%02X, want 0x80", frames[0].Payload[0])
	}
	if frames[0].Payload[1] != 0xAB {
		t.Errorf("payload[1]: got 0x%02X, want 0xAB", frames[0].Payload[1])
	}
}

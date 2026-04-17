//go:generate go run ../../../cmd/gen-registry ../../../tools/vbus_specification.vsf

package vbus

import (
	"encoding/binary"
	"fmt"
	"math"
)

type FieldType uint8

const (
	Int16  FieldType = iota
	Uint8
	Uint16
	Uint32
	Bit
)

type FieldDef struct {
	Name     string
	Offset   int
	Type     FieldType
	Factor   float64
	Unit     string
	BitIndex int
}

type PacketDef struct {
	DeviceName string
	Fields     []FieldDef
}

type Registry map[uint64]*PacketDef

func pkey(src, dst, cmd uint16) uint64 {
	return uint64(src)<<32 | uint64(dst)<<16 | uint64(cmd)
}

func (r Registry) Lookup(src, dst, cmd uint16) (*PacketDef, bool) {
	d, ok := r[pkey(src, dst, cmd)]
	return d, ok
}

func (r Registry) Register(src, dst, cmd uint16, def *PacketDef) {
	r[pkey(src, dst, cmd)] = def
}

// DefaultRegistry is populated by init() calls in registry_custom.go (custom
// devices that the Wippermann spec doesn't cover or gets wrong) and
// registry_gen.go (generated from tools/vbus_specification.vsf).
// Custom entries always take precedence over generated ones.
var DefaultRegistry = Registry{}

type TelemetryField struct {
	Name  string
	Value float64
	Unit  string
}

func Decode(f Frame, reg Registry) (deviceName string, fields []TelemetryField, known bool) {
	def, ok := reg.Lookup(f.Source, f.Destination, f.Command)
	if !ok {
		return fmt.Sprintf("src_0x%04X", f.Source), nil, false
	}

	fields = make([]TelemetryField, 0, len(def.Fields))
	for _, fd := range def.Fields {
		val, err := extractValue(f.Payload, fd)
		if err != nil {
			continue
		}
		fields = append(fields, TelemetryField{
			Name:  fd.Name,
			Value: val,
			Unit:  fd.Unit,
		})
	}

	return def.DeviceName, fields, true
}

func extractValue(payload []byte, fd FieldDef) (float64, error) {
	switch fd.Type {
	case Bit:
		if fd.Offset >= len(payload) {
			return 0, fmt.Errorf("offset %d out of range", fd.Offset)
		}
		if payload[fd.Offset]&(1<<uint(fd.BitIndex)) != 0 {
			return 1, nil
		}
		return 0, nil
	case Uint8:
		if fd.Offset >= len(payload) {
			return 0, fmt.Errorf("offset %d out of range", fd.Offset)
		}
		return math.Round(float64(payload[fd.Offset])*fd.Factor*100) / 100, nil
	case Int16:
		if fd.Offset+2 > len(payload) {
			return 0, fmt.Errorf("offset %d+2 out of range", fd.Offset)
		}
		raw := int16(binary.LittleEndian.Uint16(payload[fd.Offset:]))
		return math.Round(float64(raw)*fd.Factor*100) / 100, nil
	case Uint16:
		if fd.Offset+2 > len(payload) {
			return 0, fmt.Errorf("offset %d+2 out of range", fd.Offset)
		}
		raw := binary.LittleEndian.Uint16(payload[fd.Offset:])
		return math.Round(float64(raw)*fd.Factor*100) / 100, nil
	case Uint32:
		if fd.Offset+4 > len(payload) {
			return 0, fmt.Errorf("offset %d+4 out of range", fd.Offset)
		}
		raw := binary.LittleEndian.Uint32(payload[fd.Offset:])
		return math.Round(float64(raw)*fd.Factor*100) / 100, nil
	}
	return 0, fmt.Errorf("unknown field type %d", fd.Type)
}

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

var DefaultRegistry = Registry{
	// ── Cosmo DeltaSol BS2 / DrainBack ──────────────────────────────────────
	// Source: 0x4278  Destination: 0x0010  Command: 0x0100
	// Payload: 28 bytes (7 sub-frames)
	pkey(0x4278, 0x0010, 0x0100): {
		DeviceName: "DeltaSol BS2",
		Fields: []FieldDef{
			{Name: "temp_sensor1",     Offset: 0,  Type: Int16,  Factor: 0.1, Unit: "°C"},  // Kollektor
			{Name: "temp_sensor2",     Offset: 2,  Type: Int16,  Factor: 0.1, Unit: "°C"},  // Puffer
			{Name: "temp_sensor3",     Offset: 4,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor4",     Offset: 6,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "pump_speed_1",     Offset: 8,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "pump_speed_2",     Offset: 9,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "relay_mask",       Offset: 10, Type: Uint16, Factor: 1.0, Unit: ""},
			{Name: "error_mask",       Offset: 12, Type: Uint16, Factor: 1.0, Unit: ""},
			{Name: "operating_hours_1",Offset: 14, Type: Uint32, Factor: 1.0, Unit: "h"},
			{Name: "operating_hours_2",Offset: 18, Type: Uint32, Factor: 1.0, Unit: "h"},
			{Name: "heat_quantity",    Offset: 22, Type: Uint32, Factor: 1.0, Unit: "Wh"},
		},
	},

	// ── DeltaSol BS (Resol) ──────────────────────────────────────────────────
	// Source: 0x7112  Destination: 0x0010  Command: 0x0100
	pkey(0x7112, 0x0010, 0x0100): {
		DeviceName: "DeltaSol BS",
		Fields: []FieldDef{
			{Name: "temp_sensor1",    Offset: 0,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor2",    Offset: 2,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "pump_speed",      Offset: 8,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "operating_hours", Offset: 10, Type: Uint32, Factor: 1.0, Unit: "h"},
			{Name: "error_mask",      Offset: 14, Type: Uint16, Factor: 1.0, Unit: ""},
		},
	},

	// ── DeltaSol BS Plus (Resol) ─────────────────────────────────────────────
	// Source: 0x7110  Destination: 0x0010  Command: 0x0100
	pkey(0x7110, 0x0010, 0x0100): {
		DeviceName: "DeltaSol BS Plus",
		Fields: []FieldDef{
			{Name: "temp_sensor1",    Offset: 0,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor2",    Offset: 2,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor3",    Offset: 4,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor4",    Offset: 6,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "pump_speed1",     Offset: 8,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "pump_speed2",     Offset: 9,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "relay_mask",      Offset: 10, Type: Uint16, Factor: 1.0, Unit: ""},
			{Name: "heat_quantity",   Offset: 12, Type: Uint32, Factor: 1.0, Unit: "Wh"},
			{Name: "error_mask",      Offset: 16, Type: Uint16, Factor: 1.0, Unit: ""},
		},
	},

	// ── DeltaSol C (Resol) ───────────────────────────────────────────────────
	// Source: 0x7111  Destination: 0x0010  Command: 0x0100
	pkey(0x7111, 0x0010, 0x0100): {
		DeviceName: "DeltaSol C",
		Fields: []FieldDef{
			{Name: "temp_sensor1",    Offset: 0,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor2",    Offset: 2,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor3",    Offset: 4,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "pump_speed1",     Offset: 8,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "operating_hours", Offset: 10, Type: Uint32, Factor: 1.0, Unit: "h"},
			{Name: "error_mask",      Offset: 14, Type: Uint16, Factor: 1.0, Unit: ""},
		},
	},
}

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

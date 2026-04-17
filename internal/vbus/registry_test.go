package vbus

import (
	"encoding/binary"
	"testing"
)

func buildPayload(fields map[int]int16, size int) []byte {
	p := make([]byte, size)
	for offset, val := range fields {
		binary.LittleEndian.PutUint16(p[offset:], uint16(val))
	}
	return p
}

func TestDecode_KnownDevice(t *testing.T) {
	// 0x7112 is in the generated Wippermann registry.
	// temperature_sensor_1 @ offset 0 (Int16, factor 0.1) → 250 raw = 25.0°C
	p := make([]byte, 72) // generated entry has fields up to offset ~68
	binary.LittleEndian.PutUint16(p[0:], 250) // temperature_sensor_1
	binary.LittleEndian.PutUint16(p[2:], 350) // temperature_sensor_2

	f := Frame{Source: 0x7112, Destination: 0x0010, Command: 0x0100, Payload: p}
	_, fields, known := Decode(f, DefaultRegistry)
	if !known {
		t.Fatal("expected 0x7112 to be known in generated registry")
	}

	byName := map[string]float64{}
	for _, tf := range fields {
		byName[tf.Name] = tf.Value
	}
	if byName["temperature_sensor_1"] != 25.0 {
		t.Errorf("temperature_sensor_1: got %v, want 25.0", byName["temperature_sensor_1"])
	}
	if byName["temperature_sensor_2"] != 35.0 {
		t.Errorf("temperature_sensor_2: got %v, want 35.0", byName["temperature_sensor_2"])
	}
}

func TestDecode_UnknownDevice(t *testing.T) {
	f := Frame{Source: 0x9999, Destination: 0x0010, Command: 0x0100}
	_, _, known := Decode(f, DefaultRegistry)
	if known {
		t.Error("expected unknown device to return known=false")
	}
}

func TestDecode_NegativeTemperature(t *testing.T) {
	p := make([]byte, 72)
	v := int16(-50)
	binary.LittleEndian.PutUint16(p[0:], uint16(v)) // -5.0°C

	f := Frame{Source: 0x7112, Destination: 0x0010, Command: 0x0100, Payload: p}
	_, fields, _ := Decode(f, DefaultRegistry)

	for _, tf := range fields {
		if tf.Name == "temperature_sensor_1" {
			if tf.Value != -5.0 {
				t.Errorf("temperature_sensor_1: got %v, want -5.0", tf.Value)
			}
			return
		}
	}
	t.Error("temperature_sensor_1 not found in fields")
}

func TestDecode_BS2AllFields(t *testing.T) {
	// DeltaSol BS2: src=0x4278, 28 bytes payload
	p := make([]byte, 28)
	binary.LittleEndian.PutUint16(p[0:], 800)  // temp_sensor1 → 80.0°C
	binary.LittleEndian.PutUint32(p[14:], 100) // operating_hours_1

	f := Frame{Source: 0x4278, Destination: 0x0010, Command: 0x0100, Payload: p}
	name, fields, known := Decode(f, DefaultRegistry)
	if !known {
		t.Fatal("DeltaSol BS2 not found")
	}
	if name != "DeltaSol BS2" {
		t.Errorf("name: %q", name)
	}

	byName := map[string]float64{}
	for _, tf := range fields {
		byName[tf.Name] = tf.Value
	}
	if byName["temp_sensor1"] != 80.0 {
		t.Errorf("temp_sensor1: got %v, want 80.0", byName["temp_sensor1"])
	}
	if byName["operating_hours_1"] != 100 {
		t.Errorf("operating_hours_1: got %v, want 100", byName["operating_hours_1"])
	}
}

func TestRegistry_LookupMiss(t *testing.T) {
	r := Registry{}
	_, ok := r.Lookup(0x0001, 0x0002, 0x0003)
	if ok {
		t.Error("expected miss on empty registry")
	}
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := Registry{}
	def := &PacketDef{DeviceName: "TestDevice"}
	r.Register(0x1234, 0x0010, 0x0100, def)
	got, ok := r.Lookup(0x1234, 0x0010, 0x0100)
	if !ok || got.DeviceName != "TestDevice" {
		t.Errorf("unexpected result: ok=%v name=%q", ok, got.DeviceName)
	}
	// different key → miss
	_, ok2 := r.Lookup(0x1234, 0x0010, 0x0200)
	if ok2 {
		t.Error("expected miss for different cmd")
	}
}

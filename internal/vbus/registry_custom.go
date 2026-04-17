// registry_custom.go contains device definitions that are not in the
// Wippermann vbus-specification or where the spec disagrees with the
// observed payload layout of the actual hardware.
//
// These entries are populated BEFORE registry_gen.go (alphabetical init order)
// and are never overwritten by the generator.
package vbus

func init() {
	for k, v := range customRegistry {
		DefaultRegistry[k] = v
	}
}

var customRegistry = Registry{
	// ── Device name overrides for known Resol controllers ─────────────────────
	// The Wippermann spec uses hex addresses as device names; we override with
	// human-readable names. Field definitions come from registry_gen.go.
	// NOTE: only add entries here when the name OR field layout differs from
	// the generated version.

	// ── Cosmo Multi/DeltaSol BS2 DrainBack variant ────────────────────────────
	// src=0x4278  dst=0x0010  cmd=0x0100  payload=28 bytes (7 sub-frames)
	//
	// The Wippermann spec has a different payload layout for 0x4278 than what
	// this controller actually sends. This definition is derived from live
	// packet captures and has been validated in production.
	pkey(0x4278, 0x0010, 0x0100): {
		DeviceName: "DeltaSol BS2",
		Fields: []FieldDef{
			{Name: "temp_sensor1",      Offset: 0,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor2",      Offset: 2,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor3",      Offset: 4,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "temp_sensor4",      Offset: 6,  Type: Int16,  Factor: 0.1, Unit: "°C"},
			{Name: "pump_speed_1",      Offset: 8,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "pump_speed_2",      Offset: 9,  Type: Uint8,  Factor: 1.0, Unit: "%"},
			{Name: "relay_mask",        Offset: 10, Type: Uint16, Factor: 1.0, Unit: ""},
			{Name: "error_mask",        Offset: 12, Type: Uint16, Factor: 1.0, Unit: ""},
			{Name: "operating_hours_1", Offset: 14, Type: Uint32, Factor: 1.0, Unit: "h"},
			{Name: "operating_hours_2", Offset: 18, Type: Uint32, Factor: 1.0, Unit: "h"},
			{Name: "heat_quantity",     Offset: 22, Type: Uint32, Factor: 1.0, Unit: "Wh"},
		},
	},
}

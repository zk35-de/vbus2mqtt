// gen-registry parses the Resol VSF specification file and generates
// internal/vbus/registry_gen.go with all known packet/field definitions.
//
// Usage:
//
//	go run ./cmd/gen-registry tools/vbus_specification.vsf
//
// Or via go:generate (see internal/vbus/registry.go).
package main

import (
	"encoding/binary"
	"fmt"
	"go/format"
	"log"
	"math"
	"os"
	"strings"
	"unicode"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: gen-registry <vbus_specification.vsf>")
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatalf("read vsf: %v", err)
	}

	spec, err := parseVSF(data)
	if err != nil {
		log.Fatalf("parse vsf: %v", err)
	}

	src, err := generate(spec)
	if err != nil {
		log.Fatalf("generate: %v", err)
	}

	out := "internal/vbus/registry_gen.go"
	if err := os.WriteFile(out, src, 0644); err != nil {
		log.Fatalf("write %s: %v", out, err)
	}
	log.Printf("wrote %s (%d packets)", out, len(spec.packets))
}

// ── VSF structures ────────────────────────────────────────────────────────────

type vsf struct {
	texts   []string
	packets []vsPacket
}

type vsPacket struct {
	dst     uint16
	dstMask uint16
	src     uint16
	srcMask uint16
	cmd     uint16
	name    string
	fields  []vsField
}

type vsField struct {
	id        string
	name      string
	unitText  string
	precision int32
	typeID    int32
	parts     []vsPart
}

type vsPart struct {
	offset    uint32
	bitPos    int8
	mask      uint8
	isSigned  bool
	factor    float64
	rawFactor int64
}

// ── parser ────────────────────────────────────────────────────────────────────

func u16(b []byte, off int) uint16 { return binary.LittleEndian.Uint16(b[off:]) }
func i32(b []byte, off int) int32  { return int32(binary.LittleEndian.Uint32(b[off:])) }
func u32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }

func parseVSF(data []byte) (*vsf, error) {
	if len(data) < 0x10 {
		return nil, fmt.Errorf("file too short")
	}

	// All offsets in the VSF are absolute from start of file.
	rd16 := func(off int) uint16 { return u16(data, off) }
	rd32 := func(off int) int32  { return i32(data, off) }
	rdu32 := func(off int) uint32 { return u32(data, off) }

	specOff := int(rd32(0x0C))
	if specOff+0x2C > len(data) {
		return nil, fmt.Errorf("spec block out of range")
	}

	textCount    := int(rd32(specOff + 0x04))
	textTableOff := int(rd32(specOff + 0x08))
	ltCount      := int(rd32(specOff + 0x0C))
	ltTableOff   := int(rd32(specOff + 0x10))
	unitCount    := int(rd32(specOff + 0x14))
	unitTableOff := int(rd32(specOff + 0x18))
	pkCount      := int(rd32(specOff + 0x24))
	pkTableOff   := int(rd32(specOff + 0x28))

	_ = ltCount

	// texts: each entry is a 4-byte absolute offset to a null-terminated string
	texts := make([]string, textCount)
	for i := range textCount {
		strOff := int(rd32(textTableOff + i*4))
		end := strOff
		for end < len(data) && data[end] != 0 {
			end++
		}
		texts[i] = string(data[strOff:end])
	}

	resolveText := func(idx int32) string {
		if idx < 0 || int(idx) >= len(texts) {
			return ""
		}
		return texts[idx]
	}

	// localized texts: each entry is 3×int32 (EN, DE, FR text indices)
	resolveLT := func(ltIdx int32) string {
		if ltIdx < 0 || ltTableOff == 0 {
			return ""
		}
		enTextIdx := rd32(ltTableOff + int(ltIdx)*0x0C)
		return resolveText(enTextIdx)
	}

	// units: map unitId → unit symbol string
	type unitRec struct{ unitTextIdx int32 }
	units := make(map[int32]unitRec, unitCount)
	for i := range unitCount {
		off := unitTableOff + i*0x10
		uid := rd32(off)
		unitTextIdx := rd32(off + 0x0C)
		units[uid] = unitRec{unitTextIdx}
	}
	resolveUnit := func(uid int32) string {
		if u, ok := units[uid]; ok {
			return resolveText(u.unitTextIdx)
		}
		return ""
	}

	packets := make([]vsPacket, 0, pkCount)
	for i := range pkCount {
		pOff := pkTableOff + i*0x14
		dst     := rd16(pOff)
		dstMask := rd16(pOff + 2)
		src     := rd16(pOff + 4)
		srcMask := rd16(pOff + 6)
		cmd     := rd16(pOff + 8)
		fieldCount    := int(rd32(pOff + 0x0C))
		fieldTableOff := int(rd32(pOff + 0x10))

		name := fmt.Sprintf("0x%04X", src)

		fields := make([]vsField, 0, fieldCount)
		for j := range fieldCount {
			fOff   := fieldTableOff + j*0x1C
			nameIdx   := rd32(fOff + 0x04)
			unitID    := rd32(fOff + 0x08)
			precision := rd32(fOff + 0x0C)
			typeID    := rd32(fOff + 0x10)
			partCount    := int(rd32(fOff + 0x14))
			partTableOff := int(rd32(fOff + 0x18))

			fieldName := resolveLT(nameIdx)
			if fieldName == "" {
				fieldName = fmt.Sprintf("field_%d", j)
			}

			parts := make([]vsPart, 0, partCount)
			for k := range partCount {
				ptOff    := partTableOff + k*0x10
				offset   := rdu32(ptOff)
				bitPos   := int8(data[ptOff+4])
				mask     := data[ptOff+5]
				isSigned := data[ptOff+6] != 0
				rawFactor := int64(binary.LittleEndian.Uint64(data[ptOff+8:]))
				factor    := float64(rawFactor) * 1e-9
				parts = append(parts, vsPart{
					offset: offset, bitPos: bitPos, mask: mask,
					isSigned: isSigned, factor: factor, rawFactor: rawFactor,
				})
			}
			fields = append(fields, vsField{
				name: fieldName, unitText: resolveUnit(unitID),
				precision: precision, typeID: typeID, parts: parts,
			})
		}
		packets = append(packets, vsPacket{
			dst: dst, dstMask: dstMask,
			src: src, srcMask: srcMask,
			cmd: cmd, name: name, fields: fields,
		})
	}

	return &vsf{texts: texts, packets: packets}, nil
}

// ── code generator ────────────────────────────────────────────────────────────

func generate(spec *vsf) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString(`// Code generated by cmd/gen-registry. DO NOT EDIT.
// Source: tools/vbus_specification.vsf (Daniel Wippermann resol-vbus)
// Regenerate: go run ./cmd/gen-registry tools/vbus_specification.vsf

package vbus

import "math"

func init() {
	for k, v := range generatedRegistry {
		if _, exists := DefaultRegistry[k]; !exists {
			DefaultRegistry[k] = v
		}
	}
}

var generatedRegistry = Registry{
`)

	for _, pk := range spec.packets {
		if len(pk.fields) == 0 {
			continue
		}
		// only standard broadcast packets (dst 0x0010, cmd 0x0100)
		if pk.dst != 0x0010 || pk.cmd != 0x0100 {
			continue
		}
		// skip mask-based entries (wildcards) – handle exact matches only
		if pk.srcMask != 0xFFFF || pk.dstMask != 0xFFFF {
			continue
		}

		var fieldLines []string
		for _, f := range pk.fields {
			ft, offset, factor, ok := mapField(f)
			if !ok {
				continue
			}
			goName := toGoFieldName(f.name)
			line := fmt.Sprintf("\t\t\t{Name: %q, Offset: %d, Type: %s, Factor: %s, Unit: %q},",
				goName, offset, ft, factorLit(factor), f.unitText)
			fieldLines = append(fieldLines, line)
		}
		if len(fieldLines) == 0 {
			continue
		}

		fmt.Fprintf(&sb, "\t// %s src=0x%04X dst=0x%04X cmd=0x%04X\n", pk.name, pk.src, pk.dst, pk.cmd)
		fmt.Fprintf(&sb, "\tpkey(0x%04X, 0x%04X, 0x%04X): {\n", pk.src, pk.dst, pk.cmd)
		fmt.Fprintf(&sb, "\t\tDeviceName: %q,\n", pk.name)
		fmt.Fprintf(&sb, "\t\tFields: []FieldDef{\n")
		for _, l := range fieldLines {
			fmt.Fprintln(&sb, l)
		}
		fmt.Fprintf(&sb, "\t\t},\n\t},\n")
	}

	sb.WriteString("}\n")

	src := []byte(sb.String())
	// remove unused math import if no Pow10 calls generated
	if !strings.Contains(string(src), "math.") {
		src = []byte(strings.Replace(string(src), "\nimport \"math\"\n", "\n", 1))
	}
	return format.Source(src)
}

// mapField converts a VSF field to our FieldDef parameters.
// VSF rawFactor is the byte-position multiplier (1, 256, 65536, 16777216).
// The actual value factor = 10^(-precision).
func mapField(f vsField) (typeName string, offset uint32, factor float64, ok bool) {
	if len(f.parts) == 0 {
		return "", 0, 0, false
	}
	scale := math.Pow10(-int(f.precision))
	p0 := f.parts[0]

	switch len(f.parts) {
	case 1:
		if p0.bitPos != 0 || p0.mask == 0 {
			return "", 0, 0, false // bit field – not supported in simple registry
		}
		if p0.mask != 0xFF {
			return "", 0, 0, false // partial byte – skip
		}
		// single byte
		if p0.isSigned {
			return "Int16", p0.offset, scale, true // treat as Int16 (sign-extends)
		}
		return "Uint8", p0.offset, scale, true

	case 2:
		// Low byte (factor=1) + high byte (factor=256) → Int16/Uint16
		if p0.rawFactor != 1 || f.parts[1].rawFactor != 256 {
			return "", 0, 0, false
		}
		if p0.mask != 0xFF || f.parts[1].mask != 0xFF {
			return "", 0, 0, false
		}
		if f.parts[1].isSigned {
			return "Int16", p0.offset, scale, true
		}
		return "Uint16", p0.offset, scale, true

	case 4:
		// 4 bytes → Uint32 (offsets must be consecutive, factors 1/256/65536/16777216)
		exp := []int64{1, 256, 65536, 16777216}
		for i, p := range f.parts {
			if p.rawFactor != exp[i] || p.mask != 0xFF {
				return "", 0, 0, false
			}
		}
		return "Uint32", p0.offset, scale, true
	}
	return "", 0, 0, false
}

func factorLit(f float64) string {
	switch f {
	case 1.0:
		return "1.0"
	case 0.1:
		return "0.1"
	case 0.01:
		return "0.01"
	case 0.001:
		return "0.001"
	default:
		return fmt.Sprintf("math.Pow10(%d)", int(math.Round(math.Log10(f))))
	}
}

func toGoFieldName(s string) string {
	// "Temperature sensor 1" → "temp_sensor_1"
	var b strings.Builder
	prev := '_'
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prev = r
		} else if r == ' ' || r == '-' || r == '/' {
			if prev != '_' {
				b.WriteRune('_')
				prev = '_'
			}
		}
	}
	result := strings.TrimRight(b.String(), "_")
	if result == "" {
		return "field"
	}
	return result
}

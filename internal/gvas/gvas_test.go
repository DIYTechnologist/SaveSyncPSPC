package gvas_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"savesyncpspc/internal/gvas"
)

func fstring(value string) []byte {
	raw := append([]byte(value), 0)
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, int32(len(raw)))
	buf.Write(raw)
	return buf.Bytes()
}

func syntheticGVAS(saveClass string, payload []byte, packageUE4 uint32) []byte {
	buf := new(bytes.Buffer)
	buf.WriteString("GVAS")
	_ = binary.Write(buf, binary.LittleEndian, uint32(3))
	_ = binary.Write(buf, binary.LittleEndian, packageUE4)
	_ = binary.Write(buf, binary.LittleEndian, uint32(1008))
	_ = binary.Write(buf, binary.LittleEndian, uint16(5))
	_ = binary.Write(buf, binary.LittleEndian, uint16(4))
	_ = binary.Write(buf, binary.LittleEndian, uint16(4))
	_ = binary.Write(buf, binary.LittleEndian, uint32(12345))
	buf.Write(fstring("++UE5+Release-5.4"))
	_ = binary.Write(buf, binary.LittleEndian, uint32(3))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	buf.Write(fstring(saveClass))
	buf.WriteByte(0)
	buf.Write(payload)
	return buf.Bytes()
}

// gvasHeaderUpToEngineString builds a valid GVAS header up through the
// engine string field, for tests that need to inject a malformed value
// at a specific field beyond that point.
func gvasHeaderPrefix(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := new(bytes.Buffer)
	buf.WriteString("GVAS")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(binary.Write(buf, binary.LittleEndian, uint32(3)))     // saveVersion
	must(binary.Write(buf, binary.LittleEndian, uint32(522)))   // packageUE4
	must(binary.Write(buf, binary.LittleEndian, uint32(1008)))  // packageUE5
	must(binary.Write(buf, binary.LittleEndian, uint16(5)))     // engineMajor
	must(binary.Write(buf, binary.LittleEndian, uint16(4)))     // engineMinor
	must(binary.Write(buf, binary.LittleEndian, uint16(4)))     // enginePatch
	must(binary.Write(buf, binary.LittleEndian, uint32(12345))) // engineBuild
	return buf
}

// TestParseRejectsOversizedCustomVersionCount is a regression test: a
// customCount whose byte length (customCount*20) exceeds the file's own
// size must be rejected cleanly. Before this check was widened to int64
// arithmetic, int(customCount)*20 could wrap around on a 32-bit build
// for a large enough customCount, bypassing the bounds check entirely
// instead of failing loudly (this project only ships 64-bit binaries
// today, so the wraparound itself isn't reachable in practice, but the
// bounds check should reject an oversized count on any platform).
func TestParseRejectsOversizedCustomVersionCount(t *testing.T) {
	buf := gvasHeaderPrefix(t)
	buf.Write(fstring("++UE5+Release-5.4"))
	if err := binary.Write(buf, binary.LittleEndian, uint32(3)); err != nil { // customFormat
		t.Fatal(err)
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(0xFFFFFFFF)); err != nil { // customCount - malicious
		t.Fatal(err)
	}

	if _, err := gvas.Parse(buf.Bytes(), "malicious"); err == nil {
		t.Fatal("expected an oversized custom version count to be rejected")
	}
}

// TestParseRejectsHugeNegativeStringLength pins the exact overflow edge
// case a UTF-16 fstring length of math.MinInt32 used to risk: negating
// it in 32-bit arithmetic overflows back to itself (still negative), and
// doubling that could wrap to a small or negative byte count on a
// 32-bit build. The fix computes in int64, which has room for int32's
// full range, and must reject this length regardless of platform.
func TestParseRejectsHugeNegativeStringLength(t *testing.T) {
	buf := gvasHeaderPrefix(t)
	if err := binary.Write(buf, binary.LittleEndian, int32(math.MinInt32)); err != nil {
		t.Fatal(err)
	}

	if _, err := gvas.Parse(buf.Bytes(), "malicious"); err == nil {
		t.Fatal("expected a huge negative string length to be rejected")
	}
}

func TestParseFindsPropertiesOffsetAndClass(t *testing.T) {
	data := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("steam-properties"), 522)
	info, err := gvas.Parse(data, "steam")
	if err != nil {
		t.Fatal(err)
	}
	if got := data[info.PropertiesOffset:]; string(got) != "steam-properties" {
		t.Fatalf("payload mismatch: %q", got)
	}
	if info.PackageVersionUE4 != 522 {
		t.Fatalf("UE4 version = %d", info.PackageVersionUE4)
	}
}

func TestConvertWithEnvelopeRetainsTargetHeader(t *testing.T) {
	source := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("ps5-properties"), 522)
	template := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("pc-template"), 522)
	envelope, err := gvas.ConvertWithEnvelope(source, template, "ps5", "pc template", gvas.EnvelopeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Source.SaveClass != "/Script/Sandfall.BP_SaveGameObject_V7_C" {
		t.Fatalf("source class = %s", envelope.Source.SaveClass)
	}
	if envelope.Result.SaveClass != envelope.Target.SaveClass {
		t.Fatalf("result class = %s, target = %s", envelope.Result.SaveClass, envelope.Target.SaveClass)
	}
	if got := envelope.Data[envelope.Result.PropertiesOffset:]; string(got) != "ps5-properties" {
		t.Fatalf("payload mismatch: %q", got)
	}
	if len(envelope.Warnings) != 0 {
		t.Fatalf("warnings = %#v", envelope.Warnings)
	}
}

func TestConvertWithEnvelopeRejectsPackageMismatch(t *testing.T) {
	source := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("source"), 522)
	template := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V7_C", []byte("template"), 523)
	if _, err := gvas.ConvertWithEnvelope(source, template, "source", "template", gvas.EnvelopeOptions{}); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestConvertWithEnvelopeAllowsPackageMismatchOverride(t *testing.T) {
	source := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("source"), 522)
	template := syntheticGVAS("/Script/Sandfall.BP_SaveGameObject_V8_C", []byte("template"), 523)
	envelope, err := gvas.ConvertWithEnvelope(source, template, "source", "template", gvas.EnvelopeOptions{AllowPackageVersionMismatch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) != 1 {
		t.Fatalf("warnings = %#v", envelope.Warnings)
	}
}

package gvas_test

import (
	"bytes"
	"encoding/binary"
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

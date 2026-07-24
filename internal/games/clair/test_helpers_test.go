package clair

import (
	"bytes"
	"encoding/binary"
)

func testFString(value string) []byte {
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
	buf.Write(testFString("++UE5+Release-5.4"))
	_ = binary.Write(buf, binary.LittleEndian, uint32(3))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	buf.Write(testFString(saveClass))
	buf.WriteByte(0)
	buf.Write(payload)
	return buf.Bytes()
}


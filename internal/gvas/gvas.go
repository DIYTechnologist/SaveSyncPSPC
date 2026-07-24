package gvas

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf16"
)

type Info struct {
	Size                int     `json:"size"`
	SaveGameVersion     uint32  `json:"save_game_version"`
	PackageVersionUE4   uint32  `json:"package_version_ue4"`
	PackageVersionUE5   *uint32 `json:"package_version_ue5"`
	EngineMajor         uint16  `json:"engine_major"`
	EngineMinor         uint16  `json:"engine_minor"`
	EnginePatch         uint16  `json:"engine_patch"`
	EngineBuild         uint32  `json:"engine_build"`
	EngineString        string  `json:"engine_string"`
	CustomFormatVersion *uint32 `json:"custom_format_version"`
	CustomVersionCount  uint32  `json:"custom_version_count"`
	HeaderEnd           int     `json:"header_end"`
	SaveClass           string  `json:"save_class"`
	PropertiesOffset    int     `json:"properties_offset"`
	SHA256              string  `json:"sha256"`
}

type reader struct {
	data []byte
	pos  int
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.data) {
		return nil, fmt.Errorf("unexpected end of file at offset %#x; wanted %d bytes", r.pos, n)
	}
	value := r.data[r.pos : r.pos+n]
	r.pos += n
	return value, nil
}

func (r *reader) u8() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *reader) u16() (uint16, error) {
	b, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (r *reader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *reader) i32() (int32, error) {
	value, err := r.u32()
	return int32(value), err
}

func (r *reader) fstring() (string, error) {
	length, err := r.i32()
	if err != nil {
		return "", err
	}
	if length == 0 {
		return "", nil
	}
	if length > 0 {
		raw, err := r.take(int(length))
		if err != nil {
			return "", err
		}
		if len(raw) > 0 && raw[len(raw)-1] == 0 {
			raw = raw[:len(raw)-1]
		}
		return string(raw), nil
	}

	raw, err := r.take(int(-length) * 2)
	if err != nil {
		return "", err
	}
	if len(raw) >= 2 && raw[len(raw)-1] == 0 && raw[len(raw)-2] == 0 {
		raw = raw[:len(raw)-2]
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(raw[i:i+2]))
	}
	return string(utf16.Decode(units)), nil
}

func Parse(data []byte, label string) (Info, error) {
	r := &reader{data: data}
	magic, err := r.take(4)
	if err != nil {
		return Info{}, err
	}
	if string(magic) != "GVAS" {
		return Info{}, fmt.Errorf("%s is not an Unreal GVAS save", label)
	}

	saveVersion, err := r.u32()
	if err != nil {
		return Info{}, err
	}
	packageUE4, err := r.u32()
	if err != nil {
		return Info{}, err
	}
	var packageUE5 *uint32
	if saveVersion >= 3 && saveVersion != 34 {
		value, err := r.u32()
		if err != nil {
			return Info{}, err
		}
		packageUE5 = &value
	}

	engineMajor, err := r.u16()
	if err != nil {
		return Info{}, err
	}
	engineMinor, err := r.u16()
	if err != nil {
		return Info{}, err
	}
	enginePatch, err := r.u16()
	if err != nil {
		return Info{}, err
	}
	engineBuild, err := r.u32()
	if err != nil {
		return Info{}, err
	}
	engineString, err := r.fstring()
	if err != nil {
		return Info{}, err
	}

	var customFormat *uint32
	var customCount uint32
	if engineMajor > 4 || engineMajor == 4 && engineMinor >= 12 {
		value, err := r.u32()
		if err != nil {
			return Info{}, err
		}
		customFormat = &value
		customCount, err = r.u32()
		if err != nil {
			return Info{}, err
		}
		if _, err := r.take(int(customCount) * 20); err != nil {
			return Info{}, err
		}
	}

	headerEnd := r.pos
	saveClass, err := r.fstring()
	if err != nil {
		return Info{}, err
	}
	if engineMajor > 5 || engineMajor == 5 && engineMinor >= 4 {
		if _, err := r.u8(); err != nil {
			return Info{}, err
		}
	}
	propertiesOffset := r.pos
	if propertiesOffset >= len(data) {
		return Info{}, fmt.Errorf("%s has no property payload", label)
	}

	sum := sha256.Sum256(data)
	return Info{
		Size:                len(data),
		SaveGameVersion:     saveVersion,
		PackageVersionUE4:   packageUE4,
		PackageVersionUE5:   packageUE5,
		EngineMajor:         engineMajor,
		EngineMinor:         engineMinor,
		EnginePatch:         enginePatch,
		EngineBuild:         engineBuild,
		EngineString:        engineString,
		CustomFormatVersion: customFormat,
		CustomVersionCount:  customCount,
		HeaderEnd:           headerEnd,
		SaveClass:           saveClass,
		PropertiesOffset:    propertiesOffset,
		SHA256:              hex.EncodeToString(sum[:]),
	}, nil
}

// EnvelopeOptions controls how strictly ConvertWithEnvelope treats a
// package-version mismatch between source and target.
type EnvelopeOptions struct {
	// AllowPackageVersionMismatch downgrades a package-version mismatch
	// from a hard error to a warning. Off by default: a mismatch is the
	// one signal that the envelope graft (header bytes from the target,
	// properties from the source) might not deserialize correctly, so it
	// should only be set once a specific mismatch has been verified by
	// hand for a given game/build.
	AllowPackageVersionMismatch bool
}

// EnvelopeResult is the outcome of a successful envelope graft.
type EnvelopeResult struct {
	Data     []byte
	Source   Info
	Target   Info
	Result   Info
	Warnings []string
}

func ConvertWithEnvelope(sourceData, targetTemplate []byte, sourceLabel, targetLabel string, opts EnvelopeOptions) (EnvelopeResult, error) {
	source, err := Parse(sourceData, sourceLabel)
	if err != nil {
		return EnvelopeResult{}, err
	}
	target, err := Parse(targetTemplate, targetLabel)
	if err != nil {
		return EnvelopeResult{}, err
	}

	var warnings []string
	if source.PackageVersionUE4 != target.PackageVersionUE4 {
		msg := fmt.Sprintf("%s and %s use different UE4 package versions: %d != %d", sourceLabel, targetLabel, source.PackageVersionUE4, target.PackageVersionUE4)
		if !opts.AllowPackageVersionMismatch {
			return EnvelopeResult{}, errors.New(msg)
		}
		warnings = append(warnings, msg)
	}
	sourceUE5 := uint32(0)
	targetUE5 := uint32(0)
	if source.PackageVersionUE5 != nil {
		sourceUE5 = *source.PackageVersionUE5
	}
	if target.PackageVersionUE5 != nil {
		targetUE5 = *target.PackageVersionUE5
	}
	if (source.PackageVersionUE5 == nil) != (target.PackageVersionUE5 == nil) || sourceUE5 != targetUE5 {
		msg := fmt.Sprintf("%s and %s use different UE5 package versions: %v != %v", sourceLabel, targetLabel, source.PackageVersionUE5, target.PackageVersionUE5)
		if !opts.AllowPackageVersionMismatch {
			return EnvelopeResult{}, errors.New(msg)
		}
		warnings = append(warnings, msg)
	}

	converted := make([]byte, 0, target.PropertiesOffset+len(sourceData)-source.PropertiesOffset)
	converted = append(converted, targetTemplate[:target.PropertiesOffset]...)
	converted = append(converted, sourceData[source.PropertiesOffset:]...)
	result, err := Parse(converted, "converted "+sourceLabel)
	if err != nil {
		return EnvelopeResult{}, err
	}
	if result.SaveClass != target.SaveClass {
		return EnvelopeResult{}, fmt.Errorf("target save class was not retained for %s", sourceLabel)
	}
	// Note: we don't attempt to verify the grafted header is one the game
	// will actually accept beyond this - that can only be confirmed by
	// loading the save.
	return EnvelopeResult{Data: converted, Source: source, Target: target, Result: result, Warnings: warnings}, nil
}

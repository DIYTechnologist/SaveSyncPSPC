"""Shared Clair Obscur GVAS parsing and envelope grafting."""

from __future__ import annotations

import hashlib
import struct
from dataclasses import dataclass


class ConversionError(RuntimeError):
    """Raised when an input is missing or structurally incompatible."""


@dataclass(frozen=True)
class GvasInfo:
    size: int
    save_game_version: int
    package_version_ue4: int
    package_version_ue5: int | None
    engine_major: int
    engine_minor: int
    engine_patch: int
    engine_build: int
    engine_string: str
    custom_format_version: int | None
    custom_version_count: int
    header_end: int
    save_class: str
    properties_offset: int
    sha256: str


class Reader:
    def __init__(self, data: bytes):
        self.data = data
        self.pos = 0

    def take(self, length: int) -> bytes:
        end = self.pos + length
        if length < 0 or end > len(self.data):
            raise ConversionError(f"Unexpected end of file at offset {self.pos:#x}; wanted {length} bytes")
        value = self.data[self.pos:end]
        self.pos = end
        return value

    def unpack(self, fmt: str) -> int:
        return struct.unpack(fmt, self.take(struct.calcsize(fmt)))[0]

    def u8(self) -> int:
        return self.unpack("<B")

    def u16(self) -> int:
        return self.unpack("<H")

    def u32(self) -> int:
        return self.unpack("<I")

    def i32(self) -> int:
        return self.unpack("<i")

    def fstring(self) -> str:
        length = self.i32()
        if length == 0:
            return ""
        if length > 0:
            raw = self.take(length)
            if raw.endswith(b"\x00"):
                raw = raw[:-1]
            return raw.decode("utf-8", errors="strict")

        raw = self.take((-length) * 2)
        if raw.endswith(b"\x00\x00"):
            raw = raw[:-2]
        return raw.decode("utf-16-le", errors="strict")


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def parse_gvas(data: bytes, label: str = "save") -> GvasInfo:
    reader = Reader(data)
    if reader.take(4) != b"GVAS":
        raise ConversionError(f"{label} is not an Unreal GVAS save")

    save_version = reader.u32()
    package_ue4 = reader.u32()
    package_ue5 = None
    if save_version >= 3 and save_version != 34:
        package_ue5 = reader.u32()

    engine_major = reader.u16()
    engine_minor = reader.u16()
    engine_patch = reader.u16()
    engine_build = reader.u32()
    engine_string = reader.fstring()

    custom_format_version = None
    custom_count = 0
    if (engine_major, engine_minor) >= (4, 12):
        custom_format_version = reader.u32()
        custom_count = reader.u32()
        reader.take(custom_count * 20)

    header_end = reader.pos
    save_class = reader.fstring()

    if (engine_major, engine_minor) >= (5, 4):
        reader.u8()
    properties_offset = reader.pos

    if properties_offset >= len(data):
        raise ConversionError(f"{label} has no property payload")

    return GvasInfo(
        size=len(data),
        save_game_version=save_version,
        package_version_ue4=package_ue4,
        package_version_ue5=package_ue5,
        engine_major=engine_major,
        engine_minor=engine_minor,
        engine_patch=engine_patch,
        engine_build=engine_build,
        engine_string=engine_string,
        custom_format_version=custom_format_version,
        custom_version_count=custom_count,
        header_end=header_end,
        save_class=save_class,
        properties_offset=properties_offset,
        sha256=sha256(data),
    )


def convert_with_envelope(
    source_data: bytes,
    target_template: bytes,
    source_label: str,
    target_label: str,
) -> tuple[bytes, GvasInfo, GvasInfo, GvasInfo]:
    """Place source properties inside the target platform's GVAS envelope."""
    source = parse_gvas(source_data, source_label)
    target = parse_gvas(target_template, target_label)

    if source.package_version_ue4 != target.package_version_ue4:
        raise ConversionError(
            f"{source_label} and {target_label} use different UE4 package versions: "
            f"{source.package_version_ue4} != {target.package_version_ue4}"
        )
    if source.package_version_ue5 != target.package_version_ue5:
        raise ConversionError(
            f"{source_label} and {target_label} use different UE5 package versions: "
            f"{source.package_version_ue5} != {target.package_version_ue5}"
        )

    converted = target_template[: target.properties_offset] + source_data[source.properties_offset :]
    result = parse_gvas(converted, f"converted {source_label}")

    if result.save_class != target.save_class:
        raise ConversionError(f"Target save class was not retained for {source_label}")
    if converted[result.properties_offset :] != source_data[source.properties_offset :]:
        raise ConversionError(f"Property payload verification failed for {source_label}")

    return converted, source, target, result


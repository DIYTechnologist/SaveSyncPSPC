import importlib.util
import struct
from pathlib import Path

import pytest

from garlicsync.clair.common import ConversionError, parse_gvas
from garlicsync.clair.pc import convert_with_pc_envelope
from garlicsync.clair.ps5 import convert_with_target_envelope


def fstring(value: str) -> bytes:
    encoded = value.encode("utf-8") + b"\x00"
    return struct.pack("<i", len(encoded)) + encoded


def gvas(save_class: str, payload: bytes, package_ue4: int = 522, package_ue5: int = 1008) -> bytes:
    return b"".join(
        [
            b"GVAS",
            struct.pack("<I", 3),
            struct.pack("<I", package_ue4),
            struct.pack("<I", package_ue5),
            struct.pack("<HHHI", 5, 4, 4, 12345),
            fstring("++UE5+Release-5.4"),
            struct.pack("<I", 3),
            struct.pack("<I", 0),
            fstring(save_class),
            b"\x00",
            payload,
        ]
    )


def load_plugin(name: str):
    path = Path(__file__).resolve().parents[1] / "src" / "garlicsync" / "games" / name
    spec = importlib.util.spec_from_file_location(f"test_plugin_{name}", path)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_parse_gvas_finds_properties_offset_and_class() -> None:
    data = gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"steam-properties")

    info = parse_gvas(data, "steam")

    assert info.save_class.endswith("BP_SaveGameObject_V8_C")
    assert data[info.properties_offset :] == b"steam-properties"
    assert info.package_version_ue4 == 522
    assert info.package_version_ue5 == 1008


def test_pc_envelope_retains_pc_header_and_uses_ps5_payload() -> None:
    ps5_data = gvas("/Script/Sandfall.BP_SaveGameObject_V7_C", b"ps5-properties")
    pc_template = gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"pc-template")

    converted, source, template, result = convert_with_pc_envelope(
        ps5_data,
        pc_template,
        "ps5",
        "pc template",
    )

    assert source.save_class.endswith("BP_SaveGameObject_V7_C")
    assert template.save_class.endswith("BP_SaveGameObject_V8_C")
    assert result.save_class == template.save_class
    assert converted[result.properties_offset :] == b"ps5-properties"


def test_ps5_envelope_retains_ps5_header_and_uses_pc_payload() -> None:
    pc_data = gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"pc-properties")
    ps5_template = gvas("/Script/Sandfall.BP_SaveGameObject_V7_C", b"ps5-template")

    converted, source, template, result = convert_with_target_envelope(
        pc_data,
        ps5_template,
        "pc",
        "ps5 template",
    )

    assert source.save_class.endswith("BP_SaveGameObject_V8_C")
    assert template.save_class.endswith("BP_SaveGameObject_V7_C")
    assert result.save_class == template.save_class
    assert converted[result.properties_offset :] == b"pc-properties"


def test_conversion_rejects_package_version_mismatch() -> None:
    source = gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"source", package_ue4=522)
    template = gvas("/Script/Sandfall.BP_SaveGameObject_V7_C", b"template", package_ue4=523)

    with pytest.raises(ConversionError, match="different UE4 package versions"):
        convert_with_target_envelope(source, template, "source", "template")


def test_clair_pc_plugin_converts_both_required_files(tmp_path: Path) -> None:
    plugin = load_plugin("clair-pc.py")
    pc_dir = tmp_path / "pc"
    pc_dir.mkdir()
    (pc_dir / "EXPEDITION_0.sav").write_bytes(gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"pc-main"))
    (pc_dir / "SavesContainer.sav").write_bytes(gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"pc-menu"))

    outputs, manifest = plugin.convert_from_ps5(
        {
            "gameplay": gvas("/Script/Sandfall.BP_SaveGameObject_V7_C", b"ps5-main"),
            "container": gvas("/Script/Sandfall.BP_SaveGameObject_V7_C", b"ps5-menu"),
        },
        pc_dir,
    )

    main_info = parse_gvas(outputs["EXPEDITION_0.sav"], "main output")
    container_info = parse_gvas(outputs["SavesContainer.sav"], "container output")
    assert outputs["EXPEDITION_0.sav"][main_info.properties_offset :] == b"ps5-main"
    assert outputs["SavesContainer.sav"][container_info.properties_offset :] == b"ps5-menu"
    assert manifest["warnings"] == []


def test_clair_ps5_plugin_converts_both_required_payloads(tmp_path: Path) -> None:
    plugin = load_plugin("clair-ps5.py")
    pc_dir = tmp_path / "pc"
    pc_dir.mkdir()
    (pc_dir / "EXPEDITION_0.sav").write_bytes(gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"pc-main"))
    (pc_dir / "SavesContainer.sav").write_bytes(gvas("/Script/Sandfall.BP_SaveGameObject_V8_C", b"pc-menu"))

    outputs, manifest = plugin.convert_to_ps5(
        pc_dir,
        {
            "gameplay": gvas("/Script/Sandfall.BP_SaveGameObject_V7_C", b"ps5-main"),
            "container": gvas("/Script/Sandfall.BP_SaveGameObject_V7_C", b"ps5-menu"),
        },
    )

    main_info = parse_gvas(outputs["sdimg_EXPEDITION0"], "main output")
    container_info = parse_gvas(outputs["sdimg_SavesContainer"], "container output")
    assert outputs["sdimg_EXPEDITION0"][main_info.properties_offset :] == b"pc-main"
    assert outputs["sdimg_SavesContainer"][container_info.properties_offset :] == b"pc-menu"
    assert manifest["warnings"] == []

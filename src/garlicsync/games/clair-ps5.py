"""Clair Obscur target plugin for PS5 saves mounted by Garlic."""

from __future__ import annotations

from dataclasses import asdict
from pathlib import Path

from garlicsync.clair.ps5 import convert_with_target_envelope

TARGET = "ps5"
PAYLOAD_NAME = "ue4savegame.dpx.sav"
COMPATIBILITY = {
    "pc": {
        "platform": "Steam",
        "gameplay_class_suffix": "BP_SaveGameObject_V8_C",
        "version": "V8",
    },
    "ps5": {
        "platform": "PS5",
        "gameplay_class_suffix": "BP_SaveGameObject_V7_C",
        "version": "V7",
    },
    "convertible": True,
    "note": "Known compatible envelope graft: Steam gameplay V8 <-> PS5 gameplay V7.",
}
SAVE_IMAGES = [
    {
        "logical": "gameplay",
        "save_name": "sdimg_EXPEDITION0",
        "label": "EXPEDITION_0",
        "pc_file": "EXPEDITION_0.sav",
    },
    {
        "logical": "container",
        "save_name": "sdimg_SavesContainer",
        "label": "SavesContainer",
        "pc_file": "SavesContainer.sav",
    },
]


class PluginError(RuntimeError):
    pass


def validate_pc_dir(pc_dir: Path) -> tuple[Path, Path]:
    main = pc_dir / "EXPEDITION_0.sav"
    container = pc_dir / "SavesContainer.sav"
    missing = [str(path) for path in (main, container) if not path.is_file()]
    if missing:
        raise PluginError("Missing PC save file(s): " + ", ".join(missing))
    return main, container


def class_warnings(source: object, target: object, logical: str) -> list[str]:
    if logical != "gameplay":
        return []
    warnings: list[str] = []
    pc_suffix = COMPATIBILITY["pc"]["gameplay_class_suffix"]
    ps5_suffix = COMPATIBILITY["ps5"]["gameplay_class_suffix"]
    if not str(source.save_class).endswith(str(pc_suffix)):
        warnings.append(f"Expected Steam gameplay class ending {pc_suffix}, got {source.save_class}")
    if not str(target.save_class).endswith(str(ps5_suffix)):
        warnings.append(f"Expected PS5 gameplay class ending {ps5_suffix}, got {target.save_class}")
    return warnings


def convert_to_ps5(pc_dir: Path, ps5_templates: dict[str, bytes]) -> tuple[dict[str, bytes], dict[str, object]]:
    validate_pc_dir(pc_dir)
    replacements: dict[str, bytes] = {}
    manifest: dict[str, object] = {
        "pc_dir": str(pc_dir),
        "compatibility": COMPATIBILITY,
        "warnings": [],
    }

    for image in SAVE_IMAGES:
        logical = image["logical"]
        save_name = image["save_name"]
        label = image["label"]
        pc_path = pc_dir / image["pc_file"]
        output, source, target, result = convert_with_target_envelope(
            pc_path.read_bytes(),
            ps5_templates[logical],
            f"PC {label}",
            f"Garlic PS5 {label} template",
        )
        replacements[save_name] = output
        manifest["warnings"].extend(class_warnings(source, target, logical))
        manifest[logical] = {
            "source": asdict(source),
            "target_template": asdict(target),
            "result": asdict(result),
            "save_name": save_name,
            "payload_name": PAYLOAD_NAME,
        }

    return replacements, manifest

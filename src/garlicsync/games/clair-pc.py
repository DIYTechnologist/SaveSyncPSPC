"""Clair Obscur target plugin for PC/Steam saves."""

from __future__ import annotations

import datetime as dt
import os
import shutil
import tempfile
from dataclasses import asdict
from pathlib import Path

from garlicsync.clair.pc import convert_with_pc_envelope

TARGET = "pc"
PC_MAIN = "EXPEDITION_0.sav"
PC_CONTAINER = "SavesContainer.sav"
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


class PluginError(RuntimeError):
    pass


def atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        handle.write(data)
        handle.flush()
        os.fsync(handle.fileno())
        tmp = Path(handle.name)
    tmp.replace(path)


def validate_pc_dir(pc_dir: Path) -> tuple[Path, Path]:
    main = pc_dir / PC_MAIN
    container = pc_dir / PC_CONTAINER
    missing = [str(path) for path in (main, container) if not path.is_file()]
    if missing:
        raise PluginError("Missing PC save file(s): " + ", ".join(missing))
    return main, container


def class_warnings(main_source: object, main_template: object) -> list[str]:
    warnings: list[str] = []
    ps5_suffix = COMPATIBILITY["ps5"]["gameplay_class_suffix"]
    pc_suffix = COMPATIBILITY["pc"]["gameplay_class_suffix"]
    if not str(main_source.save_class).endswith(str(ps5_suffix)):
        warnings.append(
            f"Expected PS5 gameplay class ending {ps5_suffix}, got {main_source.save_class}"
        )
    if not str(main_template.save_class).endswith(str(pc_suffix)):
        warnings.append(
            f"Expected Steam gameplay class ending {pc_suffix}, got {main_template.save_class}"
        )
    return warnings


def convert_from_ps5(ps5_payloads: dict[str, bytes], pc_dir: Path) -> tuple[dict[str, bytes], dict[str, object]]:
    main_path, container_path = validate_pc_dir(pc_dir)
    ps5_main = ps5_payloads["gameplay"]
    ps5_container = ps5_payloads["container"]

    main_out, main_source, main_template, main_result = convert_with_pc_envelope(
        ps5_main,
        main_path.read_bytes(),
        "Garlic PS5 EXPEDITION_0",
        "PC EXPEDITION_0 template",
    )
    container_out, container_source, container_template, container_result = convert_with_pc_envelope(
        ps5_container,
        container_path.read_bytes(),
        "Garlic PS5 SavesContainer",
        "PC SavesContainer template",
    )

    outputs = {
        PC_MAIN: main_out,
        PC_CONTAINER: container_out,
    }
    manifest = {
        "pc_dir": str(pc_dir),
        "compatibility": COMPATIBILITY,
        "warnings": class_warnings(main_source, main_template),
        "main": {
            "source": asdict(main_source),
            "template": asdict(main_template),
            "result": asdict(main_result),
        },
        "container": {
            "source": asdict(container_source),
            "template": asdict(container_template),
            "result": asdict(container_result),
        },
    }
    return outputs, manifest


def install_outputs(outputs: dict[str, bytes], pc_dir: Path) -> Path:
    timestamp = dt.datetime.now().strftime("%Y%m%d_%H%M%S")
    backup_dir = pc_dir / f"_pre_garlic_sync_{timestamp}"
    backup_dir.mkdir(parents=True, exist_ok=False)
    for name in (PC_MAIN, PC_CONTAINER):
        src = pc_dir / name
        if src.exists():
            shutil.copy2(src, backup_dir / name)
        atomic_write(src, outputs[name])
    return backup_dir

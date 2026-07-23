"""Clair Obscur conversion helpers for PC/Steam output."""

from __future__ import annotations

from garlicsync.clair.common import GvasInfo, convert_with_envelope


def convert_with_pc_envelope(
    ps5_data: bytes,
    pc_template: bytes,
    source_label: str,
    template_label: str,
) -> tuple[bytes, GvasInfo, GvasInfo, GvasInfo]:
    """Place PS5 properties inside the Steam GVAS envelope."""
    return convert_with_envelope(ps5_data, pc_template, source_label, template_label)


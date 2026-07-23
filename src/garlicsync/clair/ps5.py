"""Clair Obscur conversion helpers for PS5 output."""

from __future__ import annotations

from garlicsync.clair.common import GvasInfo, convert_with_envelope


def convert_with_target_envelope(
    source_data: bytes,
    target_template: bytes,
    source_label: str,
    target_label: str,
) -> tuple[bytes, GvasInfo, GvasInfo, GvasInfo]:
    """Place PC/Steam properties inside the PS5 GVAS envelope."""
    return convert_with_envelope(source_data, target_template, source_label, target_label)


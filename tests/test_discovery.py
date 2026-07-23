from pathlib import Path

from garlicsync.cli import DEFAULT_GAMES_DIR, load_game_profiles
from garlicsync.service import Handler


def test_clair_profile_maps_ps5_title_id() -> None:
    profiles = load_game_profiles(DEFAULT_GAMES_DIR)

    clair = profiles["clair"]

    assert clair.name == "Clair Obscur: Expedition 33"
    assert "PPSA17599" in clair.title_ids


def test_supported_groups_require_all_clair_images() -> None:
    handler = Handler.__new__(Handler)
    handler.games_dir = DEFAULT_GAMES_DIR
    saves = [
        {
            "idx": 0,
            "title_id": "PPSA17599",
            "title_name": "",
            "save_name": "sdimg_EXPEDITION0",
            "type": "ps5",
            "backup": False,
            "usb": False,
            "uid": "user-a",
        },
        {
            "idx": 1,
            "title_id": "PPSA17599",
            "title_name": "",
            "save_name": "sdimg_SavesContainer",
            "type": "ps5",
            "backup": False,
            "usb": False,
            "uid": "user-a",
        },
        {
            "idx": 2,
            "title_id": "PPSA17599",
            "title_name": "",
            "save_name": "sdimg_BackupEXPEDITION0123",
            "type": "ps5",
            "backup": False,
            "usb": False,
            "uid": "user-a",
        },
        {
            "idx": 3,
            "title_id": "PPSA17599",
            "title_name": "",
            "save_name": "sdimg_EXPEDITION0",
            "type": "ps5",
            "backup": False,
            "usb": False,
            "uid": "missing-container",
        },
    ]

    groups = handler.supported_groups(saves)

    assert len(groups) == 1
    assert groups[0]["game"] == "clair"
    assert groups[0]["uid"] == "user-a"
    assert [image["save_name"] for image in groups[0]["images"]] == [
        "sdimg_EXPEDITION0",
        "sdimg_SavesContainer",
    ]


def test_default_games_dir_is_packaged_under_src() -> None:
    assert Path(__file__).resolve().parents[1] / "src" / "garlicsync" / "games" == DEFAULT_GAMES_DIR

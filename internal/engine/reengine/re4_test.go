package reengine

import (
	"encoding/json"
	"testing"

	"savesyncpspc/internal/engine"
	"savesyncpspc/internal/gameapi"
	re "savesyncpspc/internal/reengine"
)

const re4Prefix = "sdimg_SAVESERVICE-LINE-0-"

func re4TestConfig(t *testing.T) Config {
	t.Helper()
	raw := json.RawMessage(`{
		"title": "re4",
		"save_name_prefix": "` + re4Prefix + `",
		"images": [{"logical":"save","label":"Save","dynamic_save_name":true,"dynamic_payload":true,"dynamic_pc_file":true}]
	}`)
	cfg, err := New().ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.(Config)
}

func TestParseConfigAcceptsRE4(t *testing.T) {
	re4TestConfig(t) // fails the test via t.Fatal if rejected
}

// TestConvertRE4ToPS5RequiresSteamID covers the one thing genuinely new
// about RE4 versus RE2/RE3: its PC-side cipher has no fixed key at all,
// so a run with no --steam-id must fail clearly rather than attempt a
// decrypt that can't possibly succeed.
func TestConvertRE4ToPS5RequiresSteamID(t *testing.T) {
	cfg := re4TestConfig(t)
	image := gameapi.SaveImage{Logical: "save", SaveName: re4Prefix + "0", PCFile: "data000.bin"}
	_, err := convertRE4ToPS5(cfg, image, []byte("irrelevant, should fail before reading this"))
	if err == nil {
		t.Fatal("expected an error when SteamID is unset")
	}
}

func TestConvertRE4FromPS5RequiresSteamID(t *testing.T) {
	cfg := re4TestConfig(t)
	image := gameapi.SaveImage{Logical: "save", SaveName: re4Prefix + "0"}
	_, err := convertRE4FromPS5(cfg, image, []byte("irrelevant, should fail before reading this"))
	if err == nil {
		t.Fatal("expected an error when SteamID is unset")
	}
}

// TestInspectRE4PCSideStructuralCheck covers Inspect's special-cased
// path for RE4's Lime-encrypted PC side: it can only check the header
// shape (no Steam ID available to fully decrypt), and must still catch
// a genuinely wrong file.
func TestInspectRE4PCSideStructuralCheck(t *testing.T) {
	cfg := re4TestConfig(t)

	valid, err := re.LimeEncode([]byte("arbitrary body content, at least one block worth is not required"), 1)
	if err != nil {
		t.Fatal(err)
	}
	v := New().Inspect(cfg, "save", valid, engine.SidePC, nil)
	if !v.Portable {
		t.Fatalf("expected a valid Lime header to pass structurally, got %+v", v)
	}

	v = New().Inspect(cfg, "save", []byte("not a save at all"), engine.SidePC, nil)
	if v.Portable {
		t.Fatal("expected garbage to fail")
	}

	// A well-formed but non-Lime (plain Blowfish) container must also be
	// rejected on the PC side - RE4's Steam build never uses this shape.
	blowfish, err := re.Build([]byte("XXXXXXXX"), re.KeyRE2, re.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v = New().Inspect(cfg, "save", blowfish, engine.SidePC, nil)
	if v.Portable {
		t.Fatal("expected a non-Lime container to fail RE4's PC-side check")
	}
}

// TestInspectRE4PS5SideUsesBlowfish covers the PS5 side, which is
// unaffected by RE4's PC-side Lime cipher - it's plain Blowfish exactly
// like RE2's.
func TestInspectRE4PS5SideUsesBlowfish(t *testing.T) {
	cfg := re4TestConfig(t)
	data, err := re.Build([]byte("XXXXXXXX"), re.KeyRE4, re.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v := New().Inspect(cfg, "save", data, engine.SidePS5, nil)
	if !v.Portable {
		t.Fatalf("expected a real KeyRE4-encrypted container to pass, got %+v", v)
	}
}

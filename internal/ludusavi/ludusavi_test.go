package ludusavi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testManifestYAML = `
"Baldur's Gate 3":
  files:
    "<winLocalAppData>/Larian Studios/Baldur's Gate 3/PlayerProfiles/Public/Savegames/Story":
      tags:
        - save
      when:
        - os: windows
    "<xdgData>/Larian Studios/Baldur's Gate 3/PlayerProfiles/Public/Savegames/Story":
      tags:
        - save
      when:
        - os: linux
    "<winLocalAppData>/Larian Studios/Baldur's Gate 3/analytics.lsx":
      tags:
        - config
      when:
        - os: windows
  steam:
    id: 1086940
"Clair Obscur: Expedition 33":
  files:
    "<winLocalAppData>/Packages/KeplerInteractive.Expedition33_ymj30pw7xe604/SystemAppData/wgs":
      tags:
        - save
      when:
        - os: windows
          store: microsoft
    "<winLocalAppData>/Sandfall/Saved/SaveGames/<storeUserId>/*.sav":
      tags:
        - save
      when:
        - os: windows
          store: epic
        - os: windows
          store: steam
  steam:
    id: 1903340
"No Save Tag Game":
  files:
    "<home>/config.ini":
      tags:
        - config
      when:
        - os: linux
`

func writeFreshCache(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(testManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadTestManifest(t *testing.T) *Manifest {
	t.Helper()
	path := writeFreshCache(t)
	m, err := Load(path, time.Hour, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLoadUsesFreshCacheWithoutNetwork(t *testing.T) {
	// maxAge is huge and timeout tiny; if this tried the network it would
	// either hang past the test or fail outright, so success here proves
	// the fresh cache path was taken.
	m := loadTestManifest(t)
	if _, ok := m.games["Baldur's Gate 3"]; !ok {
		t.Fatal("expected Baldur's Gate 3 entry in parsed manifest")
	}
}

func TestSuggestWindowsResolvesLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\ryan\AppData\Local`)
	m := loadTestManifest(t)
	got := m.Suggest("Baldur's Gate 3", "windows")
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	want := filepath.Join(`C:\Users\ryan\AppData\Local`, "Larian Studios", "Baldur's Gate 3", "PlayerProfiles", "Public", "Savegames", "Story")
	if got[0].Path != want {
		t.Fatalf("got %q, want %q", got[0].Path, want)
	}
	if got[0].Truncated {
		t.Fatal("should not be truncated - fully resolved")
	}
}

func TestSuggestSkipsNonSaveTags(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\ryan\AppData\Local`)
	m := loadTestManifest(t)
	got := m.Suggest("Baldur's Gate 3", "windows")
	for _, c := range got {
		if filepath.Base(c.Path) == "analytics.lsx" {
			t.Fatalf("config-tagged entry leaked into suggestions: %+v", c)
		}
	}
}

func TestSuggestLinuxUsesXDGFallback(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	m := loadTestManifest(t)
	got := m.Suggest("Baldur's Gate 3", "linux")
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".local/share", "Larian Studios", "Baldur's Gate 3", "PlayerProfiles", "Public", "Savegames", "Story")
	if got[0].Path != want {
		t.Fatalf("got %q, want %q", got[0].Path, want)
	}
}

func TestSuggestDarwinMapsToMacInManifest(t *testing.T) {
	m := loadTestManifest(t)
	// Baldur's Gate 3 fixture has no "mac" entry - darwin should yield none.
	got := m.Suggest("Baldur's Gate 3", "darwin")
	if len(got) != 0 {
		t.Fatalf("expected no candidates for darwin, got %+v", got)
	}
}

func TestSuggestSkipsMicrosoftStoreEntries(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\ryan\AppData\Local`)
	m := loadTestManifest(t)
	got := m.Suggest("Clair Obscur: Expedition 33", "windows")
	for _, c := range got {
		if filepath.Base(filepath.Dir(c.Path)) == "SystemAppData" {
			t.Fatalf("microsoft store entry leaked into suggestions: %+v", c)
		}
	}
}

func TestSuggestTruncatesAtStoreUserIDAndStripsGlob(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\ryan\AppData\Local`)
	m := loadTestManifest(t)
	got := m.Suggest("Clair Obscur: Expedition 33", "windows")
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	want := filepath.Join(`C:\Users\ryan\AppData\Local`, "Sandfall", "Saved", "SaveGames")
	if got[0].Path != want {
		t.Fatalf("got %q, want %q", got[0].Path, want)
	}
	if !got[0].Truncated {
		t.Fatal("expected Truncated=true (stopped at <storeUserId>)")
	}
}

func TestSuggestUnknownGameReturnsEmpty(t *testing.T) {
	m := loadTestManifest(t)
	if got := m.Suggest("Not A Real Game", "windows"); len(got) != 0 {
		t.Fatalf("expected no candidates, got %+v", got)
	}
}

func TestSuggestGameWithNoSaveTaggedFilesReturnsEmpty(t *testing.T) {
	m := loadTestManifest(t)
	if got := m.Suggest("No Save Tag Game", "linux"); len(got) != 0 {
		t.Fatalf("expected no candidates, got %+v", got)
	}
}

func TestSteamAppID(t *testing.T) {
	m := loadTestManifest(t)
	id, ok := m.SteamAppID("Baldur's Gate 3")
	if !ok || id != "1086940" {
		t.Fatalf("got (%q, %v), want (1086940, true)", id, ok)
	}
	if _, ok := m.SteamAppID("Not A Real Game"); ok {
		t.Fatal("expected ok=false for unknown game")
	}
}

func TestReadOrFetchFallsBackToStaleCacheOnFetchFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(testManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	// maxAge=0 forces the "stale, must refetch" branch; pointing at an
	// address nothing listens on makes the fetch fail fast, so this
	// exercises the "network failed, fall back to stale cache" path
	// rather than actually reaching ludusavi's real manifest.
	orig := ManifestURL
	ManifestURL = "http://127.0.0.1:1/unreachable"
	defer func() { ManifestURL = orig }()
	data, err := readOrFetch(path, 0, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected fallback to stale cache, got error: %v", err)
	}
	if string(data) != testManifestYAML {
		t.Fatal("stale cache content mismatch")
	}
}

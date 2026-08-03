package pcpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"savesyncpspc/internal/ludusavi"
)

func TestExpandTemplateResolvesLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\ryan\AppData\Local`)
	got, err := expandTemplate(`%LOCALAPPDATA%\Larian Studios\Baldur's Gate 3\PlayerProfiles\Public\SaveGames\Story`)
	if err != nil {
		t.Fatal(err)
	}
	want := `C:\Users\ryan\AppData\Local\Larian Studios\Baldur's Gate 3\PlayerProfiles\Public\SaveGames\Story`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandTemplateErrorsWhenPlaceholderUnset(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	if _, err := expandTemplate(`%LOCALAPPDATA%\Foo`); err == nil {
		t.Fatal("expected error for unset %LOCALAPPDATA%")
	}
}

func TestExpandTemplateResolvesHomeTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available")
	}
	got, err := expandTemplate("~/Library/Application Support/Larian Studios/Baldur's Gate 3")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Library/Application Support/Larian Studios/Baldur's Gate 3")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLocalAppDataSuffixStripsMarker(t *testing.T) {
	got := localAppDataSuffix(`%LOCALAPPDATA%\Larian Studios\Baldur's Gate 3\SaveGames\Story`)
	want := "Larian Studios/Baldur's Gate 3/SaveGames/Story"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLocalAppDataSuffixEmptyWhenNoMarker(t *testing.T) {
	if got := localAppDataSuffix(`~/Library/Application Support/Foo`); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSuggestOnlyEvaluatesCurrentGOOS(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\ryan\AppData\Local`)
	dirs := map[string]string{
		"windows": `%LOCALAPPDATA%\Larian Studios\Baldur's Gate 3\SaveGames\Story`,
		"darwin":  "~/Library/Application Support/Larian Studios/Baldur's Gate 3",
	}
	got := Suggest(dirs, "")
	if runtime.GOOS != "windows" {
		for _, c := range got {
			if c.Reason == "windows default" {
				t.Fatalf("evaluated windows template on non-windows GOOS: %+v", c)
			}
		}
	}
}

func TestSuggestLinuxProtonCompatdata(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only behavior")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available")
	}
	dirs := map[string]string{
		"windows": `%LOCALAPPDATA%\Larian Studios\Baldur's Gate 3\PlayerProfiles\Public\SaveGames\Story`,
	}
	got := Suggest(dirs, "1086940")
	if len(got) == 0 {
		t.Fatal("expected at least one Proton compatdata candidate")
	}
	wantSuffix := filepath.Join("steamapps", "compatdata", "1086940", "pfx", "drive_c", "users", "steamuser", "AppData", "Local", "Larian Studios", "Baldur's Gate 3", "PlayerProfiles", "Public", "SaveGames", "Story")
	found := false
	for _, c := range got {
		if filepath.Join(home, ".local", "share", "Steam", wantSuffix) == c.Path {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a candidate under ~/.local/share/Steam/%s, got %+v", wantSuffix, got)
	}
}

func TestSuggestEmptyWhenNoDirsConfigured(t *testing.T) {
	if got := Suggest(nil, ""); len(got) != 0 {
		t.Fatalf("expected no candidates, got %+v", got)
	}
}

const testManifestYAML = `
"Baldur's Gate 3":
  files:
    "<xdgData>/Larian Studios/Baldur's Gate 3/PlayerProfiles/Public/Savegames/Story":
      tags:
        - save
      when:
        - os: linux
  steam:
    id: 1086940
`

func loadTestManifest(t *testing.T) *ludusavi.Manifest {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(testManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ludusavi.Load(path, time.Hour, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSuggestWithManifestMergesLudusaviCandidates(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only fixture")
	}
	manifest := loadTestManifest(t)
	got := SuggestWithManifest(nil, "", manifest, "Baldur's Gate 3")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".local/share", "Larian Studios", "Baldur's Gate 3", "PlayerProfiles", "Public", "Savegames", "Story")
	found := false
	for _, c := range got {
		if c.Path == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ludusavi-sourced candidate %q, got %+v", want, got)
	}
}

func TestSuggestWithManifestNilManifestFallsBackToStatic(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\ryan\AppData\Local`)
	dirs := map[string]string{"windows": `%LOCALAPPDATA%\Foo`}
	got := SuggestWithManifest(dirs, "", nil, "Baldur's Gate 3")
	if runtime.GOOS == "windows" {
		if len(got) != 1 {
			t.Fatalf("got %+v, want exactly the static candidate", got)
		}
	}
}

func TestSuggestWithManifestDeduplicatesAgainstStatic(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only fixture")
	}
	home, _ := os.UserHomeDir()
	staticPath := filepath.Join(home, ".local/share", "Larian Studios", "Baldur's Gate 3", "PlayerProfiles", "Public", "Savegames", "Story")
	dirs := map[string]string{"linux": staticPath}
	manifest := loadTestManifest(t)
	got := SuggestWithManifest(dirs, "", manifest, "Baldur's Gate 3")
	count := 0
	for _, c := range got {
		if c.Path == filepath.Clean(staticPath) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one deduplicated candidate for %q, got %d in %+v", staticPath, count, got)
	}
}

func TestSuggestWithManifestUsesLudusaviSteamIDWhenStaticMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only behavior")
	}
	manifest := loadTestManifest(t)
	dirs := map[string]string{"windows": `%LOCALAPPDATA%\Larian Studios\Baldur's Gate 3\PlayerProfiles\Public\SaveGames\Story`}
	got := SuggestWithManifest(dirs, "", manifest, "Baldur's Gate 3")
	found := false
	for _, c := range got {
		if strings.Contains(c.Path, "compatdata/1086940") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a Proton compatdata guess using ludusavi's steam id, got %+v", got)
	}
}

func TestDirExists(t *testing.T) {
	tmp := t.TempDir()
	if !dirExists(tmp) {
		t.Fatal("expected tmp dir to exist")
	}
	if dirExists(filepath.Join(tmp, "does-not-exist")) {
		t.Fatal("expected nonexistent path to report false")
	}
}

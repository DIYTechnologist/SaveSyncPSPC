package pcpath

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
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

func TestDirExists(t *testing.T) {
	tmp := t.TempDir()
	if !dirExists(tmp) {
		t.Fatal("expected tmp dir to exist")
	}
	if dirExists(filepath.Join(tmp, "does-not-exist")) {
		t.Fatal("expected nonexistent path to report false")
	}
}

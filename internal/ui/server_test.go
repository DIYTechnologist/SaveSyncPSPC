package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestAPIRunSerializesConcurrentJobs is a regression test for a real
// race: bridge.PrepareOutputDir deletes and recreates --output-dir, so
// two concurrent /api/run jobs against the same output directory (a
// double-clicked button, or two browser tabs) could interleave their
// RemoveAll/MkdirAll calls and corrupt or lose each other's output. This
// fires two overlapping requests at the real handler and confirms their
// underlying work never actually overlaps - proven by recording the
// wall-clock window each request's Garlic call falls in and checking the
// two windows don't intersect, which can only hold if runMu forced the
// second request's entire bridge.PS5ToPC call (including its own
// PrepareOutputDir) to wait for the first to finish completely.
func TestAPIRunSerializesConcurrentJobs(t *testing.T) {
	gamesDir := t.TempDir()
	profile := `{
		"game": "test",
		"name": "Test",
		"ids": [{"id": "PPSA00000"}],
		"engine": "unreal",
		"engine_config": {
			"module": "",
			"images": [{"logical": "gameplay", "save_name": "sdimg_TEST", "pc_file": "test.sav", "payload": "payload.sav"}]
		}
	}`
	if err := os.WriteFile(filepath.Join(gamesDir, "test.json"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}

	pcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pcDir, "test.sav"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var windows [][2]time.Time // [start, end) of each Garlic hit, in arrival order

	garlic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		time.Sleep(80 * time.Millisecond) // widen the race window
		mu.Lock()
		windows = append(windows, [2]time.Time{start, time.Now()})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"saves":[]}`) // causes FindSaveIndex to fail cleanly afterward
	}))
	defer garlic.Close()

	srv := Server{GamesDir: gamesDir}
	uiServer := httptest.NewServer(srv.Handler())
	defer uiServer.Close()

	fire := func() {
		body, _ := json.Marshal(map[string]any{
			"garlic":     garlic.URL,
			"game":       "test",
			"direction":  "ps5-to-pc",
			"pc_dir":     pcDir,
			"output_dir": t.TempDir(),
			"force":      true,
		})
		resp, err := http.Post(uiServer.URL+"/api/run", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); fire() }()
	go func() { defer wg.Done(); fire() }()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(windows) != 2 {
		t.Fatalf("expected 2 Garlic hits (one per run), got %d", len(windows))
	}
	a, b := windows[0], windows[1]
	overlap := a[0].Before(b[1]) && b[0].Before(a[1])
	if overlap {
		t.Fatalf("runs overlapped: run1=[%v,%v] run2=[%v,%v] - runMu did not serialize /api/run", a[0], a[1], b[0], b[1])
	}
}

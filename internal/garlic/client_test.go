package garlic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEndpointEncodesSpacesAsPercent20 is a regression test for a real
// transport bug: Garlic's server decodes %20 as a space but treats + as a
// literal character, so url.Values.Encode()'s +-for-space form encoding
// made every request for a space-containing filename silently target the
// wrong file (self-consistently across upload and download, which is what
// made it hard to spot). Confirmed live against a real Garlic server.
func TestEndpointEncodesSpacesAsPercent20(t *testing.T) {
	client := New("http://example.test:8082", time.Second)
	got := client.endpoint("/api/download_file", map[string]string{"name": "A Nautiloid in Hell - 0h 00m.lsv"})
	if strings.Contains(got, "+") {
		t.Fatalf("endpoint URL still uses + for spaces: %s", got)
	}
	if !strings.Contains(got, "A%20Nautiloid%20in%20Hell%20-%200h%2000m.lsv") {
		t.Fatalf("endpoint URL doesn't %%20-encode the name: %s", got)
	}
}

// TestMountParsesFileList covers the real behavior that makes dynamic
// filename discovery (e.g. Baldur's Gate 3) possible: Garlic's mount
// response includes a "files" array describing everything in the mounted
// save image (confirmed live: {"files":[{"name":...,"dir":bool,
// "size":int}]}), and Mount must parse it rather than discard it.
func TestMountParsesFileList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mount" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"title_id":"PPSA18463","files":[
			{"name":"sce_sys","dir":true,"size":65536},
			{"name":"sce_sys/param.sfo","dir":false,"size":3072},
			{"name":"A Nautiloid in Hell - 0h 00m.lsv","dir":false,"size":8740891}
		]}`)
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	files, err := client.Mount(22)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %#v, want 3 entries", files)
	}
	var lsv *MountedFile
	for i := range files {
		if files[i].Name == "A Nautiloid in Hell - 0h 00m.lsv" {
			lsv = &files[i]
		}
	}
	if lsv == nil {
		t.Fatal("expected .lsv entry in file list")
	}
	if lsv.Dir {
		t.Fatal(".lsv entry should not be a directory")
	}
	if lsv.Size != 8740891 {
		t.Fatalf("lsv.Size = %d, want 8740891", lsv.Size)
	}
}

// TestMountByNameMountsWithoutUnmounting is a regression test for the
// resolution-pass use case: MountByName must leave the save mounted (no
// implicit Unmount) so the caller can inspect the file list and only
// then decide what to download/upload - unlike FetchPayload/
// ReplacePayload, which know the filename upfront and clean up
// themselves.
func TestMountByNameMountsWithoutUnmounting(t *testing.T) {
	unmountCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/saves":
			fmt.Fprint(w, `{"saves":[{"title_id":"PPSA18463","save_name":"sdimg_Save0002","type":"ps5","uid":"u1"}]}`)
		case "/api/mount":
			fmt.Fprint(w, `{"files":[{"name":"save.lsv","dir":false,"size":100}]}`)
		case "/api/unmount":
			unmountCalled = true
			fmt.Fprint(w, `{}`)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	idx, files, err := client.MountByName([]string{"PPSA18463"}, "sdimg_Save0002", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
	if len(files) != 1 || files[0].Name != "save.lsv" {
		t.Fatalf("files = %#v", files)
	}
	if unmountCalled {
		t.Fatal("MountByName should not have called unmount")
	}
}

// TestDownloadFileAllowsJSONShapedSaveContent is a regression test: a
// response that merely looks like JSON (matches the content-type sniff
// or starts with '{') isn't necessarily Garlic's own error wrapper - it
// could be a save file whose own content happens to be JSON. Only a
// response that actually decodes with an "error" key is a real error;
// anything else must be returned as the file, not rejected.
func TestDownloadFileAllowsJSONShapedSaveContent(t *testing.T) {
	body := `{"version":2,"gameTime":15,"session":"abc"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	data, err := client.DownloadFile("gameinfo.json")
	if err != nil {
		t.Fatalf("expected a JSON-shaped save file to download successfully, got: %v", err)
	}
	if string(data) != body {
		t.Fatalf("got %q, want %q", data, body)
	}
}

// TestDownloadFileRejectsGenuineGarlicError covers the case the
// content-type/prefix sniff exists for: Garlic's own JSON error wrapper
// must still be rejected.
func TestDownloadFileRejectsGenuineGarlicError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"file not found"}`)
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	if _, err := client.DownloadFile("missing.bin"); err == nil {
		t.Fatal("expected a genuine Garlic error response to be rejected")
	} else if !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("expected the error message to surface Garlic's reason, got: %v", err)
	}
}

// TestDownloadFileAllowsBraceLeadingBinaryContent covers content whose
// first byte happens to be '{' without being valid JSON at all - the
// prefix sniff alone must not be enough to reject it.
func TestDownloadFileAllowsBraceLeadingBinaryContent(t *testing.T) {
	body := []byte("{not actually json, just happens to start with a brace")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	data, err := client.DownloadFile("data000.bin")
	if err != nil {
		t.Fatalf("expected non-JSON content to download successfully, got: %v", err)
	}
	if string(data) != string(body) {
		t.Fatalf("got %q, want %q", data, body)
	}
}

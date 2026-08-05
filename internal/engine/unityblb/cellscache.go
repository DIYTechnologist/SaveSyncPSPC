package unityblb

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// cellsCacheDir is the entry-name prefix (both inside a container and in
// the PC directory tree) for baked world-cell data: real save state -
// terrain/base edits in that region - not disposable cache, despite the
// directory's name.
const cellsCacheDir = "CellsCache/"

// batchIDPattern extracts the leading batch id from a cell filename like
// "baked-batch-cells-11-17-12.bin" -> "11". The PC client groups every
// cell sharing a batch id into one "...-grp0.zip" (observed splitting
// into further groups isn't something a single real save has shown, but
// isn't ruled out either - see groupCellsCacheIntoZips).
var batchIDPattern = regexp.MustCompile(`^baked-batch-cells-(\d+)-`)

// splitCellsCacheEntries separates a decoded container's flat entries
// into non-CellsCache entries (returned as-is) and the CellsCache ones
// (returned with the "CellsCache/" prefix stripped, ready for
// groupCellsCacheIntoZips).
func splitCellsCacheEntries(entries []Entry) (plain []Entry, cellsCache []Entry, err error) {
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, cellsCacheDir) {
			plain = append(plain, e)
			continue
		}
		base := strings.TrimPrefix(e.Name, cellsCacheDir)
		if batchIDPattern.FindStringSubmatch(base) == nil {
			return nil, nil, fmt.Errorf("unrecognized CellsCache entry name %q", e.Name)
		}
		cellsCache = append(cellsCache, Entry{Name: base, Data: e.Data})
	}
	return plain, cellsCache, nil
}

// groupCellsCacheIntoZips rebuilds the PC-shaped
// "CellsCache/baked-batch-cells-<batch>-grp0.zip" files from flattened
// per-cell entries (as found directly in a PS5 container), one zip per
// batch id, Stored (uncompressed) to match the PC client's own zips.
// Every cell for a batch id is put in a single "grp0" zip regardless of
// count: the real client's own "grp0"/"grp1"/... split is a size-bounding
// implementation detail of how it originally wrote the cache, not
// something the game's zip reader has been observed to require when
// reading it back.
func groupCellsCacheIntoZips(cellsCache []Entry) ([]Entry, error) {
	byBatch := map[string][]Entry{}
	var order []string
	for _, e := range cellsCache {
		m := batchIDPattern.FindStringSubmatch(e.Name)
		if m == nil {
			return nil, fmt.Errorf("unrecognized CellsCache entry name %q", e.Name)
		}
		batch := m[1]
		if _, ok := byBatch[batch]; !ok {
			order = append(order, batch)
		}
		byBatch[batch] = append(byBatch[batch], e)
	}
	sort.Slice(order, func(i, j int) bool {
		vi, _ := strconv.Atoi(order[i])
		vj, _ := strconv.Atoi(order[j])
		return vi < vj
	})

	out := make([]Entry, 0, len(order))
	for _, batch := range order {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, e := range byBatch[batch] {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Store})
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(e.Data); err != nil {
				return nil, err
			}
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		out = append(out, Entry{
			Name: fmt.Sprintf("%sbaked-batch-cells-%s-grp0.zip", cellsCacheDir, batch),
			Data: buf.Bytes(),
		})
	}
	return out, nil
}

// readPCDirEntries walks a PC save slot directory (e.g.
// SNAppData/SavedGames/slot0000) into the flat entry list a container
// expects: loose files (gameinfo.json, screenshot.jpg, ...) unchanged,
// and every CellsCache/*.zip's members flattened into individual
// "CellsCache/<file>.bin" entries.
func readPCDirEntries(dir string) ([]Entry, error) {
	topEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range topEntries {
		if de.IsDir() {
			if de.Name() != "CellsCache" {
				continue
			}
			zips, err := filepath.Glob(filepath.Join(dir, "CellsCache", "*.zip"))
			if err != nil {
				return nil, err
			}
			for _, zipPath := range zips {
				flattened, err := flattenCellsCacheZip(zipPath)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", zipPath, err)
				}
				out = append(out, flattened...)
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Name: de.Name(), Data: data})
	}
	return out, nil
}

func flattenCellsCacheZip(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return flattenCellsCacheZipData(data)
}

// flattenCellsCacheZipData is flattenCellsCacheZip for an already-in-memory
// zip (e.g. one just produced by groupCellsCacheIntoZips), so a round trip
// can be verified without touching disk.
func flattenCellsCacheZipData(data []byte) ([]Entry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Name: cellsCacheDir + f.Name, Data: content})
	}
	return out, nil
}

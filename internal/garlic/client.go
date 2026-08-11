package garlic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIYTechnologist/savesync-engine/util"
)

type Save map[string]any
type User map[string]any

type Client struct {
	BaseURL string
	HTTP    *http.Client

	savesOnce sync.Once
	savesData []Save
	savesErr  error
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{DialContext: safeDialContext},
		},
	}
}

// isDisallowedGarlicAddr reports whether ip is one this client should
// never connect to - link-local addresses (which includes cloud
// metadata endpoints like 169.254.169.254) and the unspecified address.
// Deliberately not blocking loopback: a legitimate local Garlic proxy
// during development is a real use case this shouldn't break, and
// loopback isn't the class of address the "no cloud metadata" concern
// is actually about.
func isDisallowedGarlicAddr(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// safeDialContext resolves addr's host and connects only to a resolved
// IP that passes isDisallowedGarlicAddr, checked at the moment of actual
// connection rather than via an earlier, separate lookup. A
// resolve-then-check-then-connect-by-hostname sequence has a real gap a
// malicious or misbehaving DNS server can exploit ("DNS rebinding"):
// answer with a safe IP for the check, then a different, disallowed one
// moments later when the connection is actually made. Resolving once
// here and dialing the specific IP literal that was just validated - not
// the hostname again - closes that gap entirely.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		if isDisallowedGarlicAddr(ip) {
			lastErr = fmt.Errorf("refusing to contact link-local/unspecified address %s (e.g. cloud metadata endpoints); point --garlic at your PS5's LAN address", ip)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no addresses found for %s", host)
	}
	return nil, lastErr
}

func (c *Client) endpoint(path string, query map[string]string) string {
	u := c.BaseURL + path
	if len(query) == 0 {
		return u
	}
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	// Garlic's server decodes %20 as a space but treats + literally, so
	// url.Values.Encode()'s form-style +-for-space encoding silently
	// reads/writes the wrong filename for any save file with a space in
	// its name (e.g. BG3's "A Nautiloid in Hell - 0h 00m.lsv"; Clair's
	// space-free payload name never tripped this). Confirmed against a
	// real Garlic server: name=A+Nautiloid... returns "File not found"
	// for a file that exists, name=A%20Nautiloid... succeeds.
	return u + "?" + strings.ReplaceAll(values.Encode(), "+", "%20")
}

func (c *Client) RequestBytes(method, path string, query map[string]string, data []byte) ([]byte, string, error) {
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.endpoint(path, query), body)
	if err != nil {
		return nil, "", err
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("Garlic request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("Garlic returned HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return raw, resp.Header.Get("Content-Type"), nil
}

func (c *Client) RequestJSON(method, path string, query map[string]string, data []byte) (map[string]any, error) {
	raw, _, err := c.RequestBytes(method, path, query, data)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("Garlic returned non-JSON response from %s", path)
	}
	if errValue, ok := value["error"]; ok && errValue != nil && fmt.Sprint(errValue) != "" {
		return nil, fmt.Errorf("%v", errValue)
	}
	return value, nil
}

// Saves fetches /api/saves and memoizes the result for the lifetime of this
// Client. Callers like FindSaveIndex, selectGame, and SupportedGroups all
// want the same snapshot within one CLI run or UI request, so repeated
// calls reuse the first successful fetch instead of round-tripping to
// Garlic again.
func (c *Client) Saves() ([]Save, error) {
	c.savesOnce.Do(func() {
		data, err := c.RequestJSON(http.MethodGet, "/api/saves", nil, nil)
		if err != nil {
			c.savesErr = err
			return
		}
		raw, ok := data["saves"].([]any)
		if !ok {
			c.savesErr = fmt.Errorf("Garlic /api/saves response did not contain a saves list")
			return
		}
		saves := make([]Save, 0, len(raw))
		for _, item := range raw {
			if save, ok := item.(map[string]any); ok {
				saves = append(saves, Save(save))
			}
		}
		c.savesData = saves
	})
	return c.savesData, c.savesErr
}

func (c *Client) Users() ([]User, error) {
	data, err := c.RequestJSON(http.MethodGet, "/api/users", nil, nil)
	if err != nil {
		return nil, err
	}
	raw, ok := data["users"].([]any)
	if !ok {
		return nil, fmt.Errorf("Garlic /api/users response did not contain a users list")
	}
	users := make([]User, 0, len(raw))
	for _, item := range raw {
		if user, ok := item.(map[string]any); ok {
			users = append(users, User(user))
		}
	}
	return users, nil
}

// MountedFile is one entry from Garlic's mount response file listing -
// confirmed live: mounting returns {"files":[{"name":...,"dir":bool,
// "size":int}, ...]} describing everything in the mounted save image.
// This is what lets a caller discover a save's actual filenames (e.g.
// Baldur's Gate 3's per-save .lsv name) rather than assuming a fixed
// convention.
type MountedFile struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

// Mount mounts the save image at idx and returns its file listing.
func (c *Client) Mount(idx int) ([]MountedFile, error) {
	data, err := c.RequestJSON(http.MethodGet, "/api/mount", map[string]string{"idx": strconv.Itoa(idx)}, nil)
	if err != nil {
		return nil, err
	}
	raw, ok := data["files"].([]any)
	if !ok {
		return nil, nil
	}
	files := make([]MountedFile, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		files = append(files, MountedFile{
			Name: fmt.Sprint(obj["name"]),
			Dir:  util.BoolValue(obj["dir"], false),
			Size: int64(toFloat(obj["size"])),
		})
	}
	return files, nil
}

func toFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

// MountByName finds the save image matching titleIDs/saveName/uid (see
// FindSaveIndex) and mounts it, returning the mounted index and its file
// listing. Unlike FetchPayload/ReplacePayload, it does not unmount when
// it returns - the caller is expected to inspect the file listing (e.g.
// to resolve a dynamic payload filename) and then explicitly Unmount
// once done, since what to download/upload isn't known until after
// seeing what's actually in the image.
func (c *Client) MountByName(titleIDs []string, saveName, uid string) (idx int, files []MountedFile, err error) {
	idx, err = c.FindSaveIndex(titleIDs, saveName, uid)
	if err != nil {
		return 0, nil, err
	}
	files, err = c.Mount(idx)
	if err != nil {
		return 0, nil, err
	}
	return idx, files, nil
}

func (c *Client) Unmount() error {
	_, err := c.RequestJSON(http.MethodGet, "/api/unmount", nil, nil)
	return err
}

func (c *Client) DownloadFile(name string) ([]byte, error) {
	raw, ctype, err := c.RequestBytes(http.MethodGet, "/api/download_file", map[string]string{"name": name}, nil)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("Garlic returned an empty response downloading %s", name)
	}
	// A response that looks like JSON might be Garlic's own error wrapper
	// rather than the requested file - but it might also just be a save
	// file whose own content happens to be (or start with) JSON. Only
	// treat it as an error when it actually decodes and carries an
	// "error" key; anything else - including a legitimate JSON save, or
	// content that merely starts with '{' without being valid JSON at
	// all - falls through and is returned as the real file.
	stripped := strings.TrimSpace(string(raw[:min(len(raw), 64)]))
	if strings.Contains(ctype, "application/json") || strings.HasPrefix(stripped, "{") {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err == nil {
			if errValue, ok := value["error"]; ok {
				return nil, fmt.Errorf("%v", errValue)
			}
		}
	}
	return raw, nil
}

func (c *Client) UploadFile(name string, data []byte) error {
	if data == nil {
		data = []byte{}
	}
	_, err := c.RequestJSON(http.MethodPost, "/api/upload_file", map[string]string{"name": name}, data)
	return err
}

func (c *Client) FindSaveIndex(titleIDs []string, saveName string, uid string) (int, error) {
	saves, err := c.Saves()
	if err != nil {
		return 0, err
	}
	titleSet := map[string]bool{}
	for _, id := range titleIDs {
		titleSet[strings.ToUpper(id)] = true
	}
	type match struct {
		idx  int
		save Save
	}
	var matches []match
	for idx, save := range saves {
		if !titleSet[strings.ToUpper(fmt.Sprint(save["title_id"]))] {
			continue
		}
		if fmt.Sprint(save["save_name"]) != saveName {
			continue
		}
		if fmt.Sprint(save["type"]) != "ps5" || util.BoolValue(save["backup"], false) || util.BoolValue(save["usb"], false) {
			continue
		}
		if uid != "" && fmt.Sprint(save["uid"]) != uid {
			continue
		}
		matches = append(matches, match{idx: idx, save: save})
	}
	if len(matches) == 0 {
		suffix := ""
		if uid != "" {
			suffix = " for uid " + uid
		}
		return 0, fmt.Errorf("could not find %s/%s%s in Garlic", strings.Join(titleIDs, ", "), saveName, suffix)
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("multiple %s saves matched; pass --ps5-uid", saveName)
	}
	return matches[0].idx, nil
}

func (c *Client) FetchPayload(titleIDs []string, saveName, payloadName, uid string) ([]byte, error) {
	idx, err := c.FindSaveIndex(titleIDs, saveName, uid)
	if err != nil {
		return nil, err
	}
	if _, err := c.Mount(idx); err != nil {
		return nil, err
	}
	defer c.Unmount()
	return c.DownloadFile(payloadName)
}

func (c *Client) ReplacePayload(titleIDs []string, saveName, payloadName string, data []byte, uid string) error {
	idx, err := c.FindSaveIndex(titleIDs, saveName, uid)
	if err != nil {
		return err
	}
	if _, err := c.Mount(idx); err != nil {
		return err
	}
	defer c.Unmount()
	return c.UploadFile(payloadName, data)
}

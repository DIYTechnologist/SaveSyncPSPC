package garlic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"savesyncpspc/internal/util"
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
		HTTP:    &http.Client{Timeout: timeout},
	}
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
	return u + "?" + values.Encode()
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

func (c *Client) Mount(idx int) error {
	_, err := c.RequestJSON(http.MethodGet, "/api/mount", map[string]string{"idx": strconv.Itoa(idx)}, nil)
	return err
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
	stripped := strings.TrimSpace(string(raw[:min(len(raw), 64)]))
	if strings.Contains(ctype, "application/json") || strings.HasPrefix(stripped, "{") {
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		if errValue, ok := value["error"]; ok {
			return nil, fmt.Errorf("%v", errValue)
		}
		return nil, fmt.Errorf("Garlic download failed")
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
	if err := c.Mount(idx); err != nil {
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
	if err := c.Mount(idx); err != nil {
		return err
	}
	defer c.Unmount()
	return c.UploadFile(payloadName, data)
}

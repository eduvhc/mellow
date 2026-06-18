package slskd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/eduvhc/mellow/internal/provider"
)

const Name = "slskd"

type Config struct {
	Host    string
	APIKey  string
	Timeout time.Duration
}

type Client struct {
	cfg    Config
	http   *http.Client
}

func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Name() string { return Name }

func (c *Client) Config() provider.ConfigSchema {
	return provider.ConfigSchema{
		Fields: []provider.ConfigField{
			{Name: "host", Label: "slskd URL", Type: "url", Required: true, Default: "http://localhost:5030"},
			{Name: "api_key", Label: "API Key", Type: "password", Required: true},
		},
	}
}

// --- API types ---

type searchRequest struct {
	SearchText string `json:"searchText"`
}

type searchResponse struct {
	ID     string         `json:"id"`
	State  string         `json:"state"`
	Responses []searchResultResponse `json:"responses"`
}

type searchResultResponse struct {
	Files []searchFile `json:"files"`
}

type searchFile struct {
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	Username   string `json:"username"`
	Path       string `json:"path"`
}

type transferResponse struct {
	Downloads []downloadResponse `json:"downloads"`
}

type downloadResponse struct {
	Username string         `json:"username"`
	Files    []downloadFile `json:"files"`
}

type downloadFile struct {
	Filename    string  `json:"filename"`
	Size        int64   `json:"size"`
	Progress    float64 `json:"progress"`
	Speed       int64   `json:"speed"`
	State       string  `json:"state"`
}

type enqueueRequest struct {
	Files []enqueueFile `json:"files"`
}

type enqueueFile struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// --- Provider implementation ---

func (c *Client) Search(ctx context.Context, query string) ([]provider.SearchResult, error) {
	body, _ := json.Marshal(searchRequest{SearchText: query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Host+"/api/v0/searches", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("slskd search: %s: %s", resp.Status, b)
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}

	return c.pollSearch(ctx, sr.ID)
}

func (c *Client) pollSearch(ctx context.Context, id string) ([]provider.SearchResult, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	deadline := time.After(60 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("slskd search %s timed out", id)
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Host+"/api/v0/searches/"+id, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("X-API-Key", c.cfg.APIKey)

			resp, err := c.http.Do(req)
			if err != nil {
				return nil, err
			}

			var sr searchResponse
			json.NewDecoder(resp.Body).Decode(&sr)
			resp.Body.Close()

			if sr.State == "Complete" || sr.State == "Stopped" {
				return c.parseResults(sr)
			}
		}
	}
}

func (c *Client) parseResults(sr searchResponse) ([]provider.SearchResult, error) {
	var results []provider.SearchResult
	for _, r := range sr.Responses {
		for _, f := range r.Files {
			q := guessQuality(f.Filename)
			results = append(results, provider.SearchResult{
				ID:       f.Username + "/" + f.Filename,
				Filename: f.Filename,
				FileSize: f.Size,
				Quality:  q,
				Source:   Name,
				Path:     f.Path,
				Metadata: map[string]any{"username": f.Username},
			})
		}
	}
	return results, nil
}

func (c *Client) Download(ctx context.Context, result provider.SearchResult) error {
	username, _ := result.Metadata["username"].(string)
	body, _ := json.Marshal(enqueueRequest{
		Files: []enqueueFile{{Filename: result.Filename, Size: result.FileSize}},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Host+"/api/v0/transfers/downloads/"+username, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slskd enqueue: %s: %s", resp.Status, b)
	}
	return nil
}

func (c *Client) Downloads(ctx context.Context) ([]provider.Download, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Host+"/api/v0/transfers/downloads", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tr transferResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}

	var downloads []provider.Download
	for _, d := range tr.Downloads {
		for _, f := range d.Files {
			dl := provider.Download{
				ID:       d.Username + "/" + f.Filename,
				Progress: f.Progress,
				Speed:    f.Speed,
				Result: provider.SearchResult{
					Filename: f.Filename,
					FileSize: f.Size,
					Source:   Name,
					Metadata: map[string]any{"username": d.Username},
				},
				Status: mapState(f.State),
			}
			downloads = append(downloads, dl)
		}
	}
	return downloads, nil
}

func (c *Client) CancelDownload(ctx context.Context, id string) error {
	// Parse id back to username/filename
	// For now, return nil — cancellation requires knowing the username
	return nil
}

func mapState(s string) provider.Status {
	switch s {
	case "Completed", "Succeeded":
		return provider.StatusCompleted
	case "Failed", "Errored":
		return provider.StatusFailed
	case "Cancelled", "Aborted":
		return provider.StatusCancelled
	case "Downloading", "Transferring", "Queued", "RemotelyQueued":
		return provider.StatusDownloading
	default:
		return provider.StatusPending
	}
}

func guessQuality(filename string) string {
	lower := filename
	if len(lower) > 4 {
		lower = lower[len(lower)-4:]
	}
	switch {
	case contains(filename, "2496") || contains(filename, "24bit") || contains(filename, "192"):
		return "flac 24/96"
	case contains(filename, "2444") || contains(filename, "24/44"):
		return "flac 24/44.1"
	case contains(filename, "1644") || contains(filename, "16/44"):
		return "flac 16/44.1"
	case extMatches(filename, ".flac"):
		return "flac"
	case extMatches(filename, ".mp3") && contains(filename, "320"):
		return "mp3 320"
	case extMatches(filename, ".mp3"):
		return "mp3"
	default:
		return "unknown"
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func extMatches(filename, ext string) bool {
	if len(filename) < len(ext) {
		return false
	}
	return filename[len(filename)-len(ext):] == ext
}

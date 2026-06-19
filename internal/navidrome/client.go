package navidrome

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Config struct {
	BaseURL  string
	Username string
	Password string
	Timeout  time.Duration
}

type Client struct {
	cfg  Config
	http *http.Client
	salt string
	token string
}

func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	salt := strconv.FormatInt(time.Now().UnixNano(), 36)
	token := fmt.Sprintf("%x", md5.Sum([]byte(cfg.Password+salt)))
	return &Client{
		cfg:   cfg,
		http:  &http.Client{Timeout: timeout},
		salt:  salt,
		token: token,
	}
}

// --- API response types ---

type apiEnvelope struct {
	Response subsonicResponse `json:"subsonic-response"`
}

type subsonicResponse struct {
	Status  string          `json:"status"`
	Version string          `json:"version"`
	Error   *subsonicError  `json:"error,omitempty"`
	// Browsing
	Artists       *ArtistsIndex  `json:"artists,omitempty"`
	Artist        *Artist        `json:"artist,omitempty"`
	Album         *Album         `json:"album,omitempty"`
	Song          *Child         `json:"song,omitempty"`
	SearchResult3 *SearchResult3 `json:"searchResult3,omitempty"`
	AlbumList2   *AlbumList2    `json:"albumList2,omitempty"`
	// Scan
	ScanStatus *ScanStatus `json:"scanStatus,omitempty"`
}

type subsonicError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ArtistsIndex struct {
	Index []Index `json:"index"`
}

type Index struct {
	Name    string  `json:"name"`
	Artists []Child `json:"artist"`
}

type Child struct {
	ID         string `json:"id"`
	ParentID   string `json:"parentId,omitempty"`
	Title      string `json:"title"`
	Name       string `json:"name,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	CoverArt   string `json:"coverArt,omitempty"`
	SongCount  int    `json:"songCount,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	Year       int    `json:"year,omitempty"`
	Genre      string `json:"genre,omitempty"`
	Path       string `json:"path,omitempty"`
	PlayCount  int    `json:"playCount,omitempty"`
}

type Artist struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	AlbumCount int   `json:"albumCount"`
	Album    []Child `json:"album"`
}

type Album struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Artist     string  `json:"artist"`
	Year       int     `json:"year"`
	Genre      string  `json:"genre"`
	CoverArt   string  `json:"coverArt"`
	SongCount  int     `json:"songCount"`
	Duration   int     `json:"duration"`
	Song       []Child `json:"song"`
}

type SearchResult3 struct {
	Artist []Child `json:"artist"`
	Album  []Child `json:"album"`
	Song   []Child `json:"song"`
}

type ScanStatus struct {
	Scanning bool `json:"scanning"`
	Count    int  `json:"count"`
}

type AlbumList2 struct {
	Album []Child `json:"album"`
}

// --- Public methods ---

func (c *Client) Ping(ctx context.Context) error {
	return c.get(ctx, "ping", nil, nil)
}

func (c *Client) GetArtists(ctx context.Context) (*ArtistsIndex, error) {
	var resp subsonicResponse
	err := c.get(ctx, "getArtists", nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Artists, nil
}

func (c *Client) GetArtist(ctx context.Context, id string) (*Artist, error) {
	var resp subsonicResponse
	err := c.get(ctx, "getArtist", url.Values{"id": {id}}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Artist, nil
}

func (c *Client) GetAlbum(ctx context.Context, id string) (*Album, error) {
	var resp subsonicResponse
	err := c.get(ctx, "getAlbum", url.Values{"id": {id}}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Album, nil
}

func (c *Client) Search(ctx context.Context, query string) (*SearchResult3, error) {
	var resp subsonicResponse
	err := c.get(ctx, "search3", url.Values{"query": {query}, "artistCount": {"20"}, "albumCount": {"20"}, "songCount": {"20"}}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.SearchResult3, nil
}

func (c *Client) GetAlbumList2(ctx context.Context, listType string, size int) (*AlbumList2, error) {
	var resp subsonicResponse
	err := c.get(ctx, "getAlbumList2", url.Values{"type": {listType}, "size": {fmt.Sprintf("%d", size)}}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.AlbumList2, nil
}

func (c *Client) StartScan(ctx context.Context) error {
	return c.get(ctx, "startScan", nil, nil)
}

func (c *Client) GetScanStatus(ctx context.Context) (*ScanStatus, error) {
	var resp subsonicResponse
	err := c.get(ctx, "getScanStatus", nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.ScanStatus, nil
}

// CoverArtURL returns the URL for album/artist cover art.
func (c *Client) CoverArtURL(id string) string {
	params := c.baseParams()
	params.Set("id", id)
	return c.cfg.BaseURL + "/rest/getCoverArt?" + params.Encode()
}

// --- Internal ---

func (c *Client) baseParams() url.Values {
	return url.Values{
		"u": {c.cfg.Username},
		"t": {c.token},
		"s": {c.salt},
		"v": {"1.16.1"},
		"c": {"mellow"},
		"f": {"json"},
	}
}

func (c *Client) get(ctx context.Context, endpoint string, extra url.Values, out *subsonicResponse) error {
	params := c.baseParams()
	for k, vs := range extra {
		for _, v := range vs {
			params.Set(k, v)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/rest/"+endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var env apiEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("navidrome: decode %s: %w", endpoint, err)
	}

	inner := env.Response
	if inner.Error != nil {
		return fmt.Errorf("navidrome: %s: %s (code %d)", endpoint, inner.Error.Message, inner.Error.Code)
	}
	if inner.Status != "ok" {
		return fmt.Errorf("navidrome: %s: status %s", endpoint, inner.Status)
	}

	if out != nil {
		*out = inner
	}
	return nil
}

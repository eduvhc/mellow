package ui

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"syscall"

	"github.com/eduvhc/mellow/internal/config"
	"github.com/eduvhc/mellow/internal/db"
	"github.com/eduvhc/mellow/internal/navidrome"
	"github.com/eduvhc/mellow/internal/provider"
	"github.com/eduvhc/mellow/templates"
)

type Handler struct {
	cfg      *config.Config
	db       *db.DB
	registry *provider.Registry
	nd       *navidrome.Client
}

func New(cfg *config.Config, database *db.DB, registry *provider.Registry, nd *navidrome.Client) *Handler {
	return &Handler{
		cfg:      cfg,
		db:       database,
		registry: registry,
		nd:       nd,
	}
}

// CombinedSearchResult holds results from both Navidrome and providers
type CombinedSearchResult struct {
	Albums    []navidrome.Child
	Tracks    []navidrome.Child
	Providers []templates.ProviderSearchResult
}

// Page routes

func (h *Handler) SearchPage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	data := templates.SearchPageData{Query: query}
	if query != "" {
		res, _ := h.searchAll(r.Context(), query)
		if res != nil {
			data.AlbumResults = res.Albums
			data.TrackResults = res.Tracks
			data.ProviderResults = res.Providers
		}
	}
	Render(w, r, templates.SearchPage(data))
}

func (h *Handler) LibraryPage(w http.ResponseWriter, r *http.Request) {
    data := templates.LibraryPageData{
        // Default to 0; populate from Navidrome when available
        ArtistsCount: 0,
        AlbumsCount:  0,
        SongsCount:   0,
    }

	if h.nd != nil {
		artists, err := h.nd.GetArtists(r.Context())
		if err == nil && artists != nil {
			count := 0
			for _, idx := range artists.Index {
				count += len(idx.Artists)
			}
			data.ArtistsCount = count
		}

		albumsResp, err := h.nd.GetAlbumList2(r.Context(), "alphabeticalByName", 10000)
		if err == nil && albumsResp != nil {
			data.AlbumsCount = len(albumsResp.Album)
			data.AlbumResults = albumsResp.Album
		}

		scanStatus, err := h.nd.GetScanStatus(r.Context())
		if err == nil && scanStatus != nil {
			data.SongsCount = scanStatus.Count
		}

		recentAlbums, err := h.nd.GetAlbumList2(r.Context(), "recent", 8)
		if err == nil && recentAlbums != nil {
			data.RecentlyPlayed = recentAlbums.Album
		}
	}

	Render(w, r, templates.LibraryPage(data))
}

func (h *Handler) DownloadsPage(w http.ResponseWriter, r *http.Request) {
	data := templates.DownloadsPageData{}

	for _, p := range h.registry.List() {
		dls, err := p.Downloads(r.Context())
		if err != nil {
			continue
		}
		for _, dl := range dls {
			if dl.Status == provider.StatusCompleted || dl.Status == provider.StatusFailed {
				data.Completed = append(data.Completed, dl)
			} else {
				data.Active = append(data.Active, dl)
			}
		}
	}

	records, err := h.db.ListDownloads()
	if err == nil {
		for _, r := range records {
			status := provider.StatusPending
			switch r.Status {
			case "completed":
				status = provider.StatusCompleted
			case "failed":
				status = provider.StatusFailed
			case "downloading":
				status = provider.StatusDownloading
			}
			dl := provider.Download{
				ID:       r.ID,
				Status:   status,
				Progress: r.Progress,
				Speed:    r.Speed,
				Result: provider.SearchResult{
					Filename: r.Filename,
					Source:   r.Provider,
				},
			}
			if status == provider.StatusCompleted || status == provider.StatusFailed {
				data.Completed = append(data.Completed, dl)
			} else {
				data.Active = append(data.Active, dl)
			}
		}
	}

	Render(w, r, templates.DownloadsPage(data))
}

func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	storageUsed, storageTotal, storagePercent := diskUsage(h.cfg.Navidrome.MusicPath)

	data := templates.SettingsPageData{
		Providers:          h.registry.List(),
		NavidromeConnected: h.nd != nil,
		NavidromeURL:       h.cfg.Navidrome.BaseURL,
		NavidromeUsername:  h.cfg.Navidrome.Username,
		StorageUsed:        storageUsed,
		StorageTotal:       storageTotal,
		StorageMusic:       storageUsed,
		StorageCache:       "—",
		StorageFree:        storageTotal,
		StoragePercent:     storagePercent,
	}

	Render(w, r, templates.SettingsPage(data))
}

// API routes

func (h *Handler) SearchAPI(w http.ResponseWriter, r *http.Request) {
	query := r.FormValue("query")
	if query == "" {
		query = r.FormValue("q")
	}
	if query == "" {
		http.Error(w, "missing query", http.StatusBadRequest)
		return
	}

	res, err := h.searchAll(r.Context(), query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := templates.SearchPageData{
		Query:           query,
		AlbumResults:    res.Albums,
		TrackResults:    res.Tracks,
		ProviderResults: res.Providers,
	}

	Render(w, r, templates.SearchResults(data))
}

func (h *Handler) searchAll(ctx context.Context, query string) (*CombinedSearchResult, error) {
	result := &CombinedSearchResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	if h.nd != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ndRes, err := h.nd.Search(ctx, query)
			if err != nil {
				return
			}
			mu.Lock()
			if ndRes.Album != nil {
				result.Albums = ndRes.Album
			}
			if ndRes.Song != nil {
				result.Tracks = ndRes.Song
			}
			mu.Unlock()
		}()
	}

	for _, p := range h.registry.List() {
		wg.Add(1)
		go func(prov provider.Provider) {
			defer wg.Done()
			results, err := prov.Search(ctx, query)
			if err != nil {
				return
			}
			mu.Lock()
			for _, sr := range results {
				title := sr.Title
				if title == "" {
					title = sr.Filename
				}
				result.Providers = append(result.Providers, templates.ProviderSearchResult{
					ID:       sr.ID,
					Title:    title,
					Artist:   sr.Artist,
					Album:    sr.Album,
					Duration: fmtDuration(int(sr.Duration.Seconds())),
					Quality:  sr.Quality,
					Source:   sr.Source,
					Filename: sr.Filename,
					FileSize: sr.FileSize,
					Username: fmt.Sprintf("%v", sr.Metadata["username"]),
				})
			}
			mu.Unlock()
		}(p)
	}

	wg.Wait()
	return result, nil
}

func (h *Handler) DownloadAPI(w http.ResponseWriter, r *http.Request) {
	providerName := r.PathValue("provider")
	id := r.FormValue("id")
	filename := r.FormValue("filename")
	sizeStr := r.FormValue("size")
	username := r.FormValue("username")

	p, ok := h.registry.Get(providerName)
	if !ok {
		http.Error(w, fmt.Sprintf("provider %q not found", providerName), http.StatusNotFound)
		return
	}

	size, _ := strconv.ParseInt(sizeStr, 10, 64)

	sr := provider.SearchResult{
		ID:       id,
		Filename: filename,
		FileSize: size,
		Source:   providerName,
		Metadata: map[string]any{"username": username},
	}

	if err := p.Download(r.Context(), sr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.db.SaveDownload(id, providerName, filename, size)

	if h.nd != nil {
		go func() {
			if err := h.nd.StartScan(context.Background()); err != nil {
				slog.Warn("navidrome scan trigger failed", "error", err)
			}
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"queued"}`))
}

func (h *Handler) DownloadsActiveAPI(w http.ResponseWriter, r *http.Request) {
	var active []provider.Download

	for _, p := range h.registry.List() {
		dls, err := p.Downloads(r.Context())
		if err != nil {
			continue
		}
		for _, dl := range dls {
			if dl.Status != provider.StatusCompleted && dl.Status != provider.StatusFailed {
				active = append(active, dl)
			}
		}
	}

	Render(w, r, templates.ActiveDownloadsList(active))
}

func (h *Handler) StorageAPI(w http.ResponseWriter, r *http.Request) {
	used, total, _ := diskUsage(h.cfg.Navidrome.MusicPath)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"used":"%s","total":"%s"}`, used, total)
}

func fmtDuration(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	m := seconds / 60
	s := seconds % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

func diskUsage(path string) (used string, total string, percent int) {
    if path == "" {
        // No path configured; return unknown values instead of mock numbers
        return "—", "—", 0
    }

	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return "—", "—", 0
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bfree * uint64(stat.Bsize)
	usedBytes := totalBytes - freeBytes

	if totalBytes == 0 {
		return "—", "—", 0
	}

	percent = int(usedBytes * 100 / totalBytes)
	used = humanBytes(usedBytes)
	total = humanBytes(totalBytes)
	return
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

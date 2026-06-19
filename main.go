package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/eduvhc/mellow/internal/config"
	"github.com/eduvhc/mellow/internal/db"
	"github.com/eduvhc/mellow/internal/navidrome"
	"github.com/eduvhc/mellow/internal/provider"
	slskdprovider "github.com/eduvhc/mellow/internal/providers/slskd"
	"github.com/eduvhc/mellow/internal/ui"
)

var (
	version = "dev"
)

func main() {
	cfgPath := flag.String("config", "mellow.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mellow %s\n", version)
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	registry := provider.NewRegistry()
	initProviders(registry, cfg)

    nd := navidrome.New(navidrome.Config{
        BaseURL:  cfg.Navidrome.BaseURL,
        Username: cfg.Navidrome.Username,
        Password: cfg.Navidrome.Password,
    })

    // Test Navidrome connection
    if err := nd.Ping(context.Background()); err != nil {
        slog.Warn("navidrome connection failed", "error", err)
        // Ensure UI reflects disconnected state
        nd = nil
    } else {
        slog.Info("navidrome connected")
    }

	handlers := ui.New(cfg, database, registry, nd)

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", handleHealth)

	// Page routes
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/search", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /search", handlers.SearchPage)
	mux.HandleFunc("GET /library", handlers.LibraryPage)
	mux.HandleFunc("GET /downloads", handlers.DownloadsPage)
	mux.HandleFunc("GET /settings", handlers.SettingsPage)

	// API routes
	mux.HandleFunc("GET /api/providers", handleListProviders(registry))
	mux.HandleFunc("POST /api/search", handlers.SearchAPI)
	mux.HandleFunc("POST /api/download/{provider}", handlers.DownloadAPI)
	mux.HandleFunc("GET /api/downloads/active", handlers.DownloadsActiveAPI)
	mux.HandleFunc("GET /api/stats", handlers.StorageAPI)
	mux.HandleFunc("GET /api/storage", handlers.StorageAPI)
	mux.HandleFunc("POST /api/navidrome/ping", handlers.TestNavidromeAPI)
	mux.HandleFunc("POST /api/provider/{provider}/ping", handlers.TestProviderAPI)
	mux.HandleFunc("GET /api/navidrome/artists", handleGetArtists(nd))
	mux.HandleFunc("GET /api/navidrome/artist/{id}", handleGetArtist(nd))
	mux.HandleFunc("GET /api/navidrome/album/{id}", handleGetAlbum(nd))
	mux.HandleFunc("POST /api/navidrome/scan", handleStartScan(nd))

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	slog.Info("mellow starting", "addr", addr, "version", version)

	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func initProviders(registry *provider.Registry, cfg *config.Config) {
	for name, pcfg := range cfg.Providers {
		if !pcfg.Enabled {
			slog.Info("provider disabled", "provider", name)
			continue
		}
		switch name {
		case "slskd":
			p := slskdprovider.New(slskdprovider.Config{
				Host:   pcfg.Options["host"],
				APIKey: pcfg.Options["api_key"],
			})
			registry.Register(p)
			slog.Info("provider registered", "provider", name)
		default:
			slog.Warn("unknown provider, skipping", "provider", name)
		}
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func handleListProviders(registry *provider.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		providers := registry.List()
		_names := make([]string, len(providers))
		for i, p := range providers {
			_names[i] = p.Name()
		}
		w.Write([]byte(`{"providers":[`))
		for i, name := range _names {
			if i > 0 {
				w.Write([]byte(","))
			}
			w.Write([]byte(fmt.Sprintf(`"%s"`, name)))
		}
		w.Write([]byte(`]}`))
	}
}

func handleGetArtists(nd *navidrome.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		artists, err := nd.GetArtists(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = artists
		w.Write([]byte(`{"status":"ok"}`))
	}
}

func handleGetArtist(nd *navidrome.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		artist, err := nd.GetArtist(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = artist
		w.Write([]byte(`{"status":"ok"}`))
	}
}

func handleGetAlbum(nd *navidrome.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		album, err := nd.GetAlbum(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = album
		w.Write([]byte(`{"status":"ok"}`))
	}
}

func handleStartScan(nd *navidrome.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := nd.StartScan(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}
}

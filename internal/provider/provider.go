package provider

import "context"

type Provider interface {
	Name() string
	Ping(ctx context.Context) error
	Search(ctx context.Context, query string) ([]SearchResult, error)
	Download(ctx context.Context, result SearchResult) error
	Downloads(ctx context.Context) ([]Download, error)
	CancelDownload(ctx context.Context, id string) error
	Config() ConfigSchema
}

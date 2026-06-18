package provider

import "time"

type Status int

const (
	StatusPending Status = iota
	StatusDownloading
	StatusCompleted
	StatusFailed
	StatusCancelled
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusDownloading:
		return "downloading"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

type SearchResult struct {
	ID       string
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
	Quality  string
	Source   string
	Filename string
	FileSize int64
	Path     string
	Metadata map[string]any
}

type Download struct {
	ID          string
	Result      SearchResult
	Status      Status
	Progress    float64
	Speed       int64
	ETA         time.Duration
	Error       error
	StartedAt   time.Time
	CompletedAt *time.Time
}

type ConfigField struct {
	Name        string
	Label       string
	Type        string
	Required    bool
	Default     string
	Description string
}

type ConfigSchema struct {
	Fields []ConfigField
}

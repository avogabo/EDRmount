package config

type DownloadProvider struct {
	Enabled bool `json:"enabled"`

	Host string `json:"host"`
	Port int    `json:"port"`
	SSL  bool   `json:"ssl"`
	User string `json:"user"`
	Pass string `json:"pass"`

	Connections      int `json:"connections"`
	PrefetchSegments int `json:"prefetch_segments"`
}

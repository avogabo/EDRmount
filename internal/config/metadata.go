package config

type TMDB struct {
	Enabled  bool   `json:"enabled"`
	APIKey   string `json:"api_key"`
	Language string `json:"language"` // e.g. "es-ES" or "en-US"
}

type Metadata struct {
	TMDB TMDB `json:"tmdb"`
}

func (m Metadata) withDefaults() Metadata {
	out := m
	// TMDB should be on by default (API key still required for real lookups).
	if !out.TMDB.Enabled {
		out.TMDB.Enabled = true
	}
	if out.TMDB.Language == "" {
		out.TMDB.Language = "es-ES"
	}
	return out
}

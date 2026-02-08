package config

// Plex config: optional library refresh after new items are imported.
//
// Note: Plex may read parts of files during scan/analysis; this can trigger on-demand streaming.
// Keep it optional.

type Plex struct {
	Enabled bool `json:"enabled"`

	BaseURL string `json:"base_url"` // e.g. http://192.168.1.10:32400
	Token   string `json:"token"`

	// PlexRoot is the root path as seen by Plex (on the Plex machine).
	// It should correspond to the mount path Plex is pointed at (usually library-auto).
	PlexRoot string `json:"plex_root"`

	// RefreshOnImport triggers a targeted refresh when an NZB is imported.
	RefreshOnImport bool `json:"refresh_on_import"`
}

func (p Plex) withDefaults() Plex {
	out := p
	return out
}

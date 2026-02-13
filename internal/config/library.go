package config

type Library struct {
	Enabled bool `json:"enabled"`

	// Root folders (inside the library mount).
	MoviesRoot string `json:"movies_root"` // e.g. PELICULAS
	SeriesRoot string `json:"series_root"` // e.g. SERIES

	EmisionFolder     string `json:"emision_folder"`     // e.g. EMISION
	FinalizadasFolder string `json:"finalizadas_folder"` // e.g. FINALIZADAS

	UppercaseFolders bool `json:"uppercase_folders"`

	// Templates (Filebot-ish). Variables are documented in SPEC.md.
	MovieDirTemplate   string `json:"movie_dir_template"`
	MovieFileTemplate  string `json:"movie_file_template"`
	SeriesDirTemplate  string `json:"series_dir_template"`
	SeriesFileTemplate string `json:"series_file_template"`

	SeasonFolderTemplate string `json:"season_folder_template"` // e.g. "TEMPORADA {season:00}"
}

func (l Library) withDefaults() Library {
	out := l
	if out.MoviesRoot == "" {
		out.MoviesRoot = "PELICULAS"
	}
	if out.SeriesRoot == "" {
		out.SeriesRoot = "SERIES"
	}
	if out.EmisionFolder == "" {
		out.EmisionFolder = "EMISION"
	}
	if out.FinalizadasFolder == "" {
		out.FinalizadasFolder = "FINALIZADAS"
	}
	if out.MovieDirTemplate == "" {
		out.MovieDirTemplate = "{movies_root}/{quality}/{initial}/{title} ({year}) tmdb-{tmdb_id}"
	}
	if out.MovieFileTemplate == "" {
		out.MovieFileTemplate = "{title} ({year}) tmdb-{tmdb_id}{ext}"
	}
	if out.SeriesDirTemplate == "" {
		out.SeriesDirTemplate = "{series_root}/{series_status}/{initial}/{series} ({year}) tmdb-{tmdb_id}"
	}
	if out.SeasonFolderTemplate == "" {
		out.SeasonFolderTemplate = "TEMPORADA {season:00}"
	}
	if out.SeriesFileTemplate == "" || out.SeriesFileTemplate == "{season:00}x{episode:00} - {episode_title}{ext}" {
		out.SeriesFileTemplate = "{series} ({year}) - {season:00}x{episode:00} - {episode_title}{ext}"
	}
	return out
}

// Defaults returns a copy of the library config with empty fields filled.
func (l Library) Defaults() Library { return l.withDefaults() }

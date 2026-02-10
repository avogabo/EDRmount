package api

import (
  "encoding/json"
  "net/http"

  "github.com/gaby/EDRmount/internal/library"
)

type templatesPreviewResp struct {
  Movie struct {
    DirTemplate  string `json:"dir_template"`
    FileTemplate string `json:"file_template"`
    ExampleDir   string `json:"example_dir"`
    ExampleFile  string `json:"example_file"`
  } `json:"movie"`
  Series struct {
    DirTemplate    string `json:"dir_template"`
    SeasonTemplate string `json:"season_template"`
    FileTemplate   string `json:"file_template"`
    ExampleDir     string `json:"example_dir"`
    ExampleSeason  string `json:"example_season"`
    ExampleFile    string `json:"example_file"`
  } `json:"series"`
  Vars map[string]string `json:"vars"`
  Nums map[string]int    `json:"nums"`
}

func (s *Server) handleTemplatesPreview(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet {
    w.WriteHeader(http.StatusMethodNotAllowed)
    return
  }
  cfg := s.Config()

  // Use configured templates (with defaults filled by config.Load).
  l := cfg.Library

  // Sample data (realistic defaults).
  vars := map[string]string{
    "movies_root":    l.MoviesRoot,
    "series_root":    l.SeriesRoot,
    "series_status":  l.EmisionFolder,
    "quality":        "1080",
    "initial":        "A",
    "title":          "Alien",
    "year":           "1979",
    "tmdb_id":        "348",
    "ext":            ".mkv",
    "series":         "Andor",
    "episode_title":  "That Would Be Me",
  }
  // Choose series status example based on configured folder names.
  if l.EmisionFolder != "" {
    vars["series_status"] = l.EmisionFolder
  }

  nums := map[string]int{
    "season":  1,
    "episode": 2,
  }

  movieDir := library.CleanPath(library.Render(l.MovieDirTemplate, vars, nums))
  movieFile := library.Render(l.MovieFileTemplate, vars, nums)

  seriesDir := library.CleanPath(library.Render(l.SeriesDirTemplate, vars, nums))
  seasonDir := library.CleanPath(library.Render(l.SeasonFolderTemplate, vars, nums))
  seriesFile := library.Render(l.SeriesFileTemplate, vars, nums)

  var resp templatesPreviewResp
  resp.Movie.DirTemplate = l.MovieDirTemplate
  resp.Movie.FileTemplate = l.MovieFileTemplate
  resp.Movie.ExampleDir = "/" + movieDir
  resp.Movie.ExampleFile = "/" + library.CleanPath(movieDir+"/"+movieFile)

  resp.Series.DirTemplate = l.SeriesDirTemplate
  resp.Series.SeasonTemplate = l.SeasonFolderTemplate
  resp.Series.FileTemplate = l.SeriesFileTemplate
  resp.Series.ExampleDir = "/" + seriesDir
  resp.Series.ExampleSeason = "/" + library.CleanPath(seriesDir+"/"+seasonDir)
  resp.Series.ExampleFile = "/" + library.CleanPath(seriesDir+"/"+seasonDir+"/"+seriesFile)

  resp.Vars = vars
  resp.Nums = nums

  w.Header().Set("Content-Type", "application/json")
  _ = json.NewEncoder(w).Encode(resp)
}

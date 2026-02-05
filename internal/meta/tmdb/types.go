package tmdb

import (
	"errors"
	"time"
)

var errInvalidDate = errors.New("invalid date")

// TMDB API docs (v3): https://developer.themoviedb.org/reference/intro/getting-started
// We keep these structs minimal and only model fields we need.

type SearchMovieResponse struct {
	Page         int                 `json:"page"`
	Results      []MovieSearchResult `json:"results"`
	TotalPages   int                 `json:"total_pages"`
	TotalResults int                 `json:"total_results"`
}

type MovieSearchResult struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	PosterPath    string `json:"poster_path"`
}

func (r MovieSearchResult) ReleaseYear() int {
	y, _ := parseYear(r.ReleaseDate)
	return y
}

type MovieDetails struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Status        string `json:"status"`
	IMDBID        string `json:"imdb_id"`
}

func (m MovieDetails) ReleaseYear() int {
	y, _ := parseYear(m.ReleaseDate)
	return y
}

type SearchTVResponse struct {
	Page         int              `json:"page"`
	Results      []TVSearchResult `json:"results"`
	TotalPages   int              `json:"total_pages"`
	TotalResults int              `json:"total_results"`
}

type TVSearchResult struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	FirstAirDate string `json:"first_air_date"`
	PosterPath   string `json:"poster_path"`
}

func (r TVSearchResult) FirstAirYear() int {
	y, _ := parseYear(r.FirstAirDate)
	return y
}

type TVDetails struct {
	ID               int           `json:"id"`
	Name             string        `json:"name"`
	OriginalName     string        `json:"original_name"`
	FirstAirDate     string        `json:"first_air_date"`
	LastAirDate      string        `json:"last_air_date"`
	Status           string        `json:"status"`
	NumberOfSeasons  int           `json:"number_of_seasons"`
	NumberOfEpisodes int           `json:"number_of_episodes"`
	Seasons          []TVSeasonRef `json:"seasons"`
}

func (t TVDetails) FirstAirYear() int {
	y, _ := parseYear(t.FirstAirDate)
	return y
}

type TVSeasonRef struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episode_count"`
	AirDate      string `json:"air_date"`
}

type TVSeasonDetails struct {
	ID           int             `json:"id"`
	SeasonNumber int             `json:"season_number"`
	Name         string          `json:"name"`
	AirDate      string          `json:"air_date"`
	Episodes     []TVEpisodeInfo `json:"episodes"`
}

type TVEpisodeInfo struct {
	ID            int    `json:"id"`
	EpisodeNumber int    `json:"episode_number"`
	SeasonNumber  int    `json:"season_number"`
	Name          string `json:"name"`
	AirDate       string `json:"air_date"`
}

func parseYear(dateYYYYMMDD string) (int, error) {
	if len(dateYYYYMMDD) < 4 {
		return 0, errInvalidDate
	}
	t, err := time.Parse("2006-01-02", dateYYYYMMDD)
	if err != nil {
		return 0, err
	}
	return t.Year(), nil
}

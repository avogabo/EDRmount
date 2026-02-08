package tmdb

import "strings"

// TMDB TV status values observed in the API include (non-exhaustive):
// - "Returning Series"
// - "Ended"
// - "Canceled"
// - "In Production"
// - "Planned"
// - "Pilot"
// - "Post Production" (rare for TV)
// - "Rumored" (rare for TV)
//
// EDRmount's library layout (per SPEC/README) uses Spanish buckets:
// - "Emision" (airing / ongoing / not finished)
// - "Finalizadas" (ended/canceled)

type SeriesBucket string

const (
	SeriesBucketUnknown    SeriesBucket = ""
	SeriesBucketEmision    SeriesBucket = "Emision"
	SeriesBucketFinalizada SeriesBucket = "Finalizadas"
)

// MapTVStatusToBucket maps TMDB tv.status to EDRmount's folder bucket.
func MapTVStatusToBucket(status string) SeriesBucket {
	s := strings.TrimSpace(strings.ToLower(status))
	s = strings.ReplaceAll(s, "_", " ")

	switch s {
	case "returning series", "in production", "planned", "pilot":
		return SeriesBucketEmision
	case "ended", "canceled", "cancelled":
		return SeriesBucketFinalizada
	default:
		return SeriesBucketUnknown
	}
}

func (b SeriesBucket) String() string { return string(b) }

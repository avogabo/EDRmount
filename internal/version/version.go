package version

// These are overridden at build time via -ldflags.
var (
	Version = "1.51"
	Commit  = "unknown"
	Date    = "unknown"
)

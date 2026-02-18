package version

// These are overridden at build time via -ldflags.
var (
	Version = "1.69"
	Commit  = "unknown"
	Date    = "unknown"
)

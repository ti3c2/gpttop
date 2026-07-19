package buildinfo

// These variables are overridden by release builds through -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return "gpttop " + Version + " commit=" + Commit + " date=" + Date
}

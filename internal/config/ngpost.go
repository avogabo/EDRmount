package config

type NgPost struct {
	Enabled bool `json:"enabled"`

	Host string `json:"host"`
	Port int    `json:"port"`
	SSL  bool   `json:"ssl"`
	User string `json:"user"`
	Pass string `json:"pass"`

	Connections int    `json:"connections"` // -n
	Threads     int    `json:"threads"`     // -t
	Groups      string `json:"groups"`      // -g comma-separated

	OutputDir string `json:"output_dir"` // where to write NZB files
	TmpDir    string `json:"tmp_dir"`    // --tmp_dir

	Obfuscate bool `json:"obfuscate"` // -x
}

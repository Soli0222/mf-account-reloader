package version

import "runtime/debug"

var Version = "dev"
var Commit = "unknown"

func Info() map[string]string {
	v, commit := Version, Commit
	modified := "false"
	if b, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && b.Main.Version != "" && b.Main.Version != "(devel)" {
			v = b.Main.Version
		}
		for _, s := range b.Settings {
			if s.Key == "vcs.revision" && commit == "unknown" {
				commit = s.Value
			}
			if s.Key == "vcs.modified" {
				modified = s.Value
			}
		}
		return map[string]string{"version": v, "commit": commit, "modified": modified, "go": b.GoVersion}
	}
	return map[string]string{"version": v, "commit": commit, "modified": modified}
}

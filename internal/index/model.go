package index

// Payload-parse types mirror the subset of Homebrew's formula/cask JSON we
// store. The upstream payload carries ~40 keys per record; we decode the ones
// that feed install + the read commands, and keep the full raw object per row
// for forward-compat (SP2–4 pull more fields with no schema change).

type payloadFormula struct {
	Name              string   `json:"name"`
	FullName          string   `json:"full_name"`
	Tap               string   `json:"tap"`
	Desc              string   `json:"desc"`
	License           string   `json:"license"`
	Homepage          string   `json:"homepage"`
	Revision          int      `json:"revision"`
	Dependencies      []string `json:"dependencies"`
	BuildDependencies []string `json:"build_dependencies"`
	Versions          struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	Bottle struct {
		Stable struct {
			Files map[string]struct {
				Cellar string `json:"cellar"`
				URL    string `json:"url"`
				SHA256 string `json:"sha256"`
			} `json:"files"`
		} `json:"stable"`
	} `json:"bottle"`
}

type payloadCask struct {
	Token    string   `json:"token"`
	Name     []string `json:"name"`
	Desc     string   `json:"desc"`
	Homepage string   `json:"homepage"`
	Version  string   `json:"version"`
	Tap      string   `json:"tap"`
}

// Formula is a query result from the index. Deps/BuildDeps are loaded only by
// callers that need them (Lookup fills them; lighter queries skip the join).
type Formula struct {
	Name          string
	FullName      string
	Tap           string
	Desc          string
	License       string
	Homepage      string
	StableVersion string
	Revision      int
	Deps          []string
	BuildDeps     []string
}

// Cask is a query result from the index.
type Cask struct {
	Token    string
	Names    []string
	Desc     string
	Homepage string
	Version  string
	Tap      string
}

// Bottle is one per-tag bottle entry for a formula. The URL is absolute (GHCR);
// SHA256 is the lowercase hex digest of the tarball.
type Bottle struct {
	Tag    string
	URL    string
	SHA256 string
	Cellar string
}

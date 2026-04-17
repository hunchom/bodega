package semver

import "github.com/Masterminds/semver/v3"

type Kind int

const (
	Unknown Kind = iota
	Major
	Minor
	Patch
)

func Diff(from, to string) Kind {
	a, err1 := semver.NewVersion(from)
	b, err2 := semver.NewVersion(to)
	if err1 != nil || err2 != nil {
		return Unknown
	}
	switch {
	case a.Major() != b.Major():
		return Major
	case a.Minor() != b.Minor():
		return Minor
	case a.Patch() != b.Patch():
		return Patch
	}
	return Unknown
}

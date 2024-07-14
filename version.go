package main

import (
	"regexp"

	"github.com/hashicorp/go-version"
)

// Same as the one from go-version, but greedier.
var versionRegexp = regexp.MustCompile(
	`v?([0-9]+(\.[0-9]+)*)` +
		`(-([0-9]+[0-9A-Za-z\-~]*(\.[0-9A-Za-z\-~]+)*)|(-?([A-Za-z\-~]+[0-9A-Za-z\-~]*(\.[0-9A-Za-z\-~]+)*)))?` +
		`(\+([0-9A-Za-z\-~]+(\.[0-9A-Za-z\-~]+)*))?`,
)

func guessVersion(name string) (*version.Version, int) {
	loc := versionRegexp.FindStringIndex(name)
	if loc == nil {
		return nil, 0
	}

	ver, err := version.NewVersion(name[loc[0]:loc[1]])
	if err != nil {
		return nil, 0
	}

	return ver, loc[0]
}

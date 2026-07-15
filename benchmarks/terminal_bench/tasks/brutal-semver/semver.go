package main

import (
	"strconv"
	"strings"
)

// Compare returns -1 if a has lower precedence than b, 0 if a and b have
// equal precedence, and +1 if a has higher precedence than b, per the
// Semantic Versioning 2.0.0 precedence rules (see
// https://semver.org/#spec-item-11). See task.yaml for the full contract.
func Compare(a, b string) int {
	av, apre := splitVersion(a)
	bv, bpre := splitVersion(b)

	if c := compareCore(av, bv); c != 0 {
		return c
	}

	if apre == "" && bpre == "" {
		return 0
	}
	// BUG: a version WITHOUT a pre-release must have HIGHER precedence than
	// the same version WITH one (e.g. 1.0.0 > 1.0.0-alpha). This has the
	// comparison backwards, so a pre-release release is treated as greater
	// than (or, transitively, at least not lower than) the plain release.
	if apre == "" {
		return -1
	}
	if bpre == "" {
		return 1
	}

	return comparePrerelease(apre, bpre)
}

// splitVersion separates the core MAJOR.MINOR.PATCH from the pre-release
// identifier string (without the leading '-').
//
// BUG: build metadata (the part starting at the first '+') is never
// stripped. When a pre-release is present it stays glued onto the
// pre-release string; when there is no pre-release, the build metadata is
// mistaken for one. Either way build metadata ends up influencing
// precedence, even though it must be ignored entirely.
func splitVersion(v string) (core [3]int, prerelease string) {
	rest := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		rest = v[:i]
		prerelease = v[i+1:]
	} else if i := strings.IndexByte(v, '+'); i >= 0 {
		rest = v[:i]
		prerelease = v[i+1:]
	}

	parts := strings.SplitN(rest, ".", 3)
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		core[i] = n
	}
	return core, prerelease
}

func compareCore(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// comparePrerelease compares two pre-release identifier strings
// (dot-separated), per semver.org rule 11.
func comparePrerelease(a, b string) int {
	aids := strings.Split(a, ".")
	bids := strings.Split(b, ".")

	n := len(aids)
	if len(bids) < n {
		n = len(bids)
	}

	for i := 0; i < n; i++ {
		// BUG: identifiers are compared purely as strings, even when both
		// are numeric. This makes "2" sort after "11" (since "1" < "2" as
		// the first byte) instead of comparing them numerically, and does
		// not apply the "numeric identifiers always have lower precedence
		// than non-numeric ones" rule.
		if aids[i] != bids[i] {
			if aids[i] < bids[i] {
				return -1
			}
			return 1
		}
	}

	// All shared identifiers are equal; the pre-release with more
	// identifiers has higher precedence.
	if len(aids) < len(bids) {
		return -1
	}
	if len(aids) > len(bids) {
		return 1
	}
	return 0
}

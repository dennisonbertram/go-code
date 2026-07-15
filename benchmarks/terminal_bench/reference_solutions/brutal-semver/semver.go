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

	// Same MAJOR.MINOR.PATCH. A version without a pre-release has higher
	// precedence than the same version with one.
	if apre == "" && bpre == "" {
		return 0
	}
	if apre == "" {
		return 1
	}
	if bpre == "" {
		return -1
	}

	return comparePrerelease(apre, bpre)
}

// splitVersion strips build metadata (everything from the first '+'
// onward, which is ignored entirely for precedence) and separates the core
// MAJOR.MINOR.PATCH from the pre-release identifier string (without the
// leading '-').
func splitVersion(v string) (core [3]int, prerelease string) {
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}

	rest := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
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
		if c := compareIdentifier(aids[i], bids[i]); c != 0 {
			return c
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

// compareIdentifier compares a single pair of dot-separated pre-release
// identifiers per semver.org rule 11.
func compareIdentifier(a, b string) int {
	an, aIsNum := numericIdentifier(a)
	bn, bIsNum := numericIdentifier(b)

	if aIsNum && bIsNum {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}

	// A numeric identifier always has lower precedence than a non-numeric
	// identifier.
	if aIsNum && !bIsNum {
		return -1
	}
	if !aIsNum && bIsNum {
		return 1
	}

	// Both non-numeric: compare lexically in ASCII order.
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// numericIdentifier reports whether s consists only of decimal digits and,
// if so, its numeric value.
func numericIdentifier(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

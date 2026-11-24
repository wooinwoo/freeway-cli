package update

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
)

func updateVersion(state State, version string) (bool, error) {
	if state.Removing {
		return false, errors.New("installation removal is in progress")
	}
	if state.Active == "" {
		return false, nil
	}
	switch compareVersions(version, state.Active) {
	case -1:
		return false, fmt.Errorf("release %s is older than installed %s; use rollback or install an explicit archive", version, state.Active)
	case 0:
		return true, nil
	default:
		return false, nil
	}
}

func compareVersions(a, b string) int {
	coreA, suffixA, _ := strings.Cut(a, "-")
	coreB, suffixB, _ := strings.Cut(b, "-")
	partsA, partsB := strings.Split(coreA, "."), strings.Split(coreB, ".")
	for i := range partsA {
		if order := compareNumeric(partsA[i], partsB[i]); order != 0 {
			return order
		}
	}
	if suffixA == "" || suffixB == "" {
		return cmp.Compare(len(suffixB), len(suffixA))
	}
	preA, preB := strings.Split(suffixA, "."), strings.Split(suffixB, ".")
	for i := range min(len(preA), len(preB)) {
		left, right := preA[i], preB[i]
		digits := func(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }
		var order int
		switch {
		case digits(left) && digits(right):
			order = compareNumeric(left, right)
		case digits(left):
			order = -1
		case digits(right):
			order = 1
		default:
			order = strings.Compare(left, right)
		}
		if order != 0 {
			return order
		}
	}
	return cmp.Compare(len(preA), len(preB))
}

func compareNumeric(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if order := cmp.Compare(len(a), len(b)); order != 0 {
		return order
	}
	return strings.Compare(a, b)
}

// NewerThan reports whether r is a strictly newer valid release than current.
func (r Release) NewerThan(current string) bool {
	return versionPattern.MatchString(current) && versionPattern.MatchString(r.Version) &&
		compareVersions(r.Version, current) > 0
}

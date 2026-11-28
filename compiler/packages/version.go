package packages

import (
	"fmt"
	"strconv"
	"strings"
)

// A semantic version such as 1.4.2, or a prefix of one such as 0.3.
type version struct {
	major, minor, patch int

	// How many of the three numbers were written, 1 to 3.
	parts int
}

// Parses a version such as `0.3.1`, `0.3` or `v1`.
func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(s, "v")
	fields := strings.Split(s, ".")
	if len(fields) > 3 {
		return version{}, false
	}
	var nums [3]int
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 || f == "" || f[0] == '+' || len(f) > 1 && f[0] == '0' {
			return version{}, false
		}
		nums[i] = n
	}
	return version{nums[0], nums[1], nums[2], len(fields)}, true
}

// Returns the version with all three numbers, such as `0.3.0`.
func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// Reports whether [v] comes before [w].
func (v version) less(w version) bool {
	if v.major != w.major {
		return v.major < w.major
	}
	if v.minor != w.minor {
		return v.minor < w.minor
	}
	return v.patch < w.patch
}

// Reports whether [v] meets the requirement [min]: the same major version,
// and not older.
func (v version) satisfies(min version) bool {
	return v.major == min.major && !v.less(min)
}

// Reports whether [v] starts with the numbers written in [prefix], so that
// `0.3.2` matches `0.3`.
func (v version) matches(prefix version) bool {
	return v.major == prefix.major &&
		(prefix.parts < 2 || v.minor == prefix.minor) &&
		(prefix.parts < 3 || v.patch == prefix.patch)
}

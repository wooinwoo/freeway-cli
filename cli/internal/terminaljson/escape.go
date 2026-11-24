// SPDX-License-Identifier: MIT

package terminaljson

import (
	"fmt"
	"strings"
	"unicode"
)

// Escape preserves JSON values while preventing terminal and direction controls.
func Escape(data []byte) string {
	var safe strings.Builder
	safe.Grow(len(data))
	start := 0
	for index, r := range string(data) {
		if r != '\r' && r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)) {
			safe.Write(data[start:index])
			fmt.Fprintf(&safe, "\\u%04x", r)
			start = index + len(string(r))
		}
	}
	safe.Write(data[start:])
	return safe.String()
}

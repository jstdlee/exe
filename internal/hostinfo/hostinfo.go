// Package hostinfo detects a friendly description of the physical machine the
// daemon runs on (e.g. "Alienware 18 Area-51", "NVIDIA DGX Spark", "MacBook
// Pro"), for the About window's tagline. Detection is OS-specific and cached;
// an empty result is fine — the UI falls back to a generic "machine".
package hostinfo

import (
	"strings"
	"sync"
)

var (
	once   sync.Once
	cached string
)

// Model returns the cached machine description, detecting it on first call.
// Priming it early (a background call at startup) keeps the About window snappy
// on platforms whose lookup shells out.
func Model() string {
	once.Do(func() { cached = dropTrailingSKU(detectModel()) })
	return cached
}

// dropTrailingSKU removes a trailing bare model/SKU code that firmware appends
// after the marketing name (e.g. "Alienware 18 Area-51 AA18250" -> "Alienware
// 18 Area-51"). Conservative: only an all-uppercase alphanumeric token that
// mixes letters and digits, and only when at least two words remain, so real
// names ("NVIDIA DGX Spark", "MacBook Pro", "Area-51") are untouched.
func dropTrailingSKU(s string) string {
	f := strings.Fields(s)
	if len(f) < 3 {
		return s
	}
	last := f[len(f)-1]
	if len(last) < 5 {
		return s
	}
	hasLetter, hasDigit := false, false
	for _, r := range last {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			return s // lowercase, hyphen, etc. — not a bare SKU
		}
	}
	if hasLetter && hasDigit {
		return strings.Join(f[:len(f)-1], " ")
	}
	return s
}

// clean normalizes whitespace (DMI often uses underscores for spaces, e.g.
// "NVIDIA_DGX_Spark") and discards the OEM placeholder strings that firmware
// ships with when a field was never programmed.
func clean(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "_", " ")), " ")
	switch strings.ToLower(s) {
	case "", "to be filled by o.e.m.", "system product name", "system manufacturer",
		"system version", "default string", "not specified", "not applicable",
		"n/a", "none", "unknown", "o.e.m.", "oem":
		return ""
	}
	return s
}

// combine joins a vendor and product into one label without repeating the
// vendor when the product already starts with it.
func combine(vendor, product string) string {
	vendor, product = clean(vendor), clean(product)
	if product == "" {
		return vendor
	}
	if vendor == "" || strings.HasPrefix(strings.ToLower(product), strings.ToLower(vendor)) {
		return product
	}
	return vendor + " " + product
}

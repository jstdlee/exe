package hostinfo

import "testing"

func TestCombine(t *testing.T) {
	cases := []struct{ vendor, product, want string }{
		{"Alienware", "Alienware 18 Area-51", "Alienware 18 Area-51"}, // no vendor dup
		{"Dell Inc.", "XPS 15 9520", "Dell Inc. XPS 15 9520"},
		{"NVIDIA", "", "NVIDIA"},
		{"NVIDIA", "NVIDIA_DGX_Spark", "NVIDIA DGX Spark"}, // DMI underscores -> spaces, no vendor dup
		{"", "MacBook Pro", "MacBook Pro"},
		{"To Be Filled By O.E.M.", "System Product Name", ""}, // both placeholders
	}
	for _, c := range cases {
		if got := combine(c.vendor, c.product); got != c.want {
			t.Errorf("combine(%q,%q) = %q, want %q", c.vendor, c.product, got, c.want)
		}
	}
}

func TestDropTrailingSKU(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Alienware 18 Area-51 AA18250", "Alienware 18 Area-51"}, // trims the SKU
		{"NVIDIA DGX Spark", "NVIDIA DGX Spark"},                 // all words readable
		{"MacBook Pro", "MacBook Pro"},                           // < 3 words, untouched
		{"Dell XPS 15 9520", "Dell XPS 15 9520"},                 // trailing all-digits, kept
		{"Lenovo ThinkPad X1 Carbon", "Lenovo ThinkPad X1 Carbon"},
	}
	for _, c := range cases {
		if got := dropTrailingSKU(c.in); got != c.want {
			t.Errorf("dropTrailingSKU(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

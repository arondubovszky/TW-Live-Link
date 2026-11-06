package main

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"1.1.0", "1.0.0", true}, {"1.0.0", "1.1.0", false}, {"1.10.0", "1.9.9", true}, {"1.1.0", "1.1.0", false}, {"2", "1.9.9", true}}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%s,%s)=%v", c.a, c.b, got)
		}
	}
}

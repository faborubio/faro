package indicator

import "testing"

func TestCadenceValid(t *testing.T) {
	cases := []struct {
		c    Cadence
		want bool
	}{
		{CadenceDaily, true},
		{CadenceMonthly, true},
		{Cadence(""), false},
		{Cadence("weekly"), false},
	}
	for _, tc := range cases {
		if got := tc.c.Valid(); got != tc.want {
			t.Errorf("Cadence(%q).Valid() = %v, quiero %v", tc.c, got, tc.want)
		}
	}
}

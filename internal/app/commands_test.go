package app

import "testing"

func TestSplitCommands(t *testing.T) {
	cases := map[string]struct {
		in   []string
		want []string
	}{
		"single":           {[]string{"uptime"}, []string{"uptime"}},
		"comma separated":  {[]string{"a,b,c"}, []string{"a", "b", "c"}},
		"repeated flag":    {[]string{"a", "b"}, []string{"a", "b"}},
		"mixed":            {[]string{"a,b", "c"}, []string{"a", "b", "c"}},
		"trims whitespace": {[]string{" a , b "}, []string{"a", "b"}},
		"drops blanks":     {[]string{"a,,b", ""}, []string{"a", "b"}},
		"nil input":        {nil, nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := SplitCommands(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

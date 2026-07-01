package lines

import "testing"

func TestClassify_LevelAndPace(t *testing.T) {
	cases := []struct {
		name        string
		used, limit float64
		breaches    bool
		want        Severity
	}{
		{"healthy", 50, 100, false, SevOK},
		{"warn at 80% level", 80, 100, false, SevWarn},
		{"crit at 95% level", 95, 100, false, SevCrit},
		{"crit when already over", 120, 100, false, SevCrit},
		{"pace breach warns below level", 60, 100, true, SevWarn},
		{"level dominates pace", 96, 100, true, SevCrit},
		{"zero usage is healthy", 0, 100, false, SevOK},
		{"no limit is OK", 10, 0, false, SevOK},
	}
	for _, c := range cases {
		if got := Classify(c.used, c.limit, c.breaches); got != c.want {
			t.Errorf("%s: Classify(%v,%v,%v)=%v, want %v", c.name, c.used, c.limit, c.breaches, got, c.want)
		}
	}
}

func TestSeverityHex(t *testing.T) {
	if got := SevOK.Hex(); got != "" {
		t.Errorf("OK should carry no color, got %q", got)
	}
	if got := SevWarn.Hex(); got != "#f59e0b" {
		t.Errorf("warn hex = %q, want amber", got)
	}
	if got := SevCrit.Hex(); got != "#ef4444" {
		t.Errorf("crit hex = %q, want red", got)
	}
}

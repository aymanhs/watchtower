package metric

import "testing"

func TestSplitInstance(t *testing.T) {
	cases := []struct {
		name         string
		wantBaseKey  string
		wantInstance string
	}{
		{"used", "used", ""},
		{"root.used", "used", "root"},
		{"root.used_percent", "used_percent", "root"},
		// The split is always at the *last* dot, however many others
		// come before it — an instance name itself containing a "."
		// still resolves correctly.
		{"mnt.v1.used", "used", "mnt.v1"},
		// A trailing or leading dot with nothing on one side isn't a
		// real instance split.
		{"used.", "used.", ""},
		{".used", ".used", ""},
	}
	for _, tc := range cases {
		baseKey, instance := SplitInstance(tc.name)
		if baseKey != tc.wantBaseKey || instance != tc.wantInstance {
			t.Errorf("SplitInstance(%q) = (%q, %q), want (%q, %q)", tc.name, baseKey, instance, tc.wantBaseKey, tc.wantInstance)
		}
	}
}

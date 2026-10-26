package task

import "testing"

func TestTruncateUTF8PreservesValidEncoding(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"ascii", "quota exceeded", "quota exceeded"},
		{"callback", "配额已用尽", "配额已用尽"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateUTF8(tc.input, 1000)
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestTruncateUTF8RepairsInvalidInput(t *testing.T) {
	got := truncateUTF8("bad\xffvalue", 1000)
	if want := "bad\uFFFDvalue"; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

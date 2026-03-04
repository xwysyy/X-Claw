package utils

import "testing"

func TestCanonicalSessionKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"agent:main:main", "agent:main:main"},
		{" Agent:Main:Main ", "agent:main:main"},
		{"\nconv:MAIN\r\n", "conv:main"},
	}

	for _, tt := range tests {
		if got := CanonicalSessionKey(tt.in); got != tt.want {
			t.Fatalf("CanonicalSessionKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

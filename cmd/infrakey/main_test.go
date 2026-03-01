package main

import "testing"

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"10", 10},
		{"2KB", 2 * 1024},
		{"3MB", 3 * 1024 * 1024},
		{"4GB", 4 * 1024 * 1024 * 1024},
		{"5GiB", 5 * 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		got, err := parseByteSize(tc.in)
		if err != nil {
			t.Fatalf("parseByteSize(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseByteSize(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseChunkSizeDefaultFullCopy(t *testing.T) {
	got, err := parseChunkSize("", true)
	if err != nil {
		t.Fatalf("parseChunkSize unexpected error: %v", err)
	}
	want := int64(2 * 1024 * 1024 * 1024)
	if got != want {
		t.Fatalf("parseChunkSize default full-copy = %d want %d", got, want)
	}
}

func TestNormalizeBundlePathInput(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"./vault.bundle", "vault.bundle"},
		{"./vault.bundle.parts", "vault.bundle"},
		{"./vault.bundle.parts/", "vault.bundle"},
		{"/tmp/a/vault.bundle.parts", "/tmp/a/vault.bundle"},
	}
	for _, tc := range cases {
		got := normalizeBundlePathInput(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeBundlePathInput(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

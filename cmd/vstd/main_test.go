package main

import "testing"

func TestModuleVersionMatchesRevision(t *testing.T) {
	revision := "d9085b65de8fe48874cb93b63052825f06e18ce8"
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "matching pseudo version", version: "v0.0.0-20260902193254-d9085b65de8f", want: true},
		{name: "different revision", version: "v0.0.0-20260902193254-aaaaaaaaaaaa"},
		{name: "tag has no verifiable revision", version: "v0.4.0"},
		{name: "development build", version: "(devel)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := moduleVersionMatchesRevision(test.version, revision); got != test.want {
				t.Fatalf("moduleVersionMatchesRevision(%q, %q) = %v, want %v", test.version, revision, got, test.want)
			}
		})
	}
}

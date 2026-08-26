package collab

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse battery staple") {
		t.Fatal("valid password was rejected")
	}
	if CheckPassword(h, "wrong password") {
		t.Fatal("invalid password was accepted")
	}
}

func TestPasswordPolicy(t *testing.T) {
	if _, err := HashPassword("too-short"); err == nil {
		t.Fatal("short password was accepted")
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("  Our 2027 Strategy!  "); got != "our-2027-strategy" {
		t.Fatalf("Slug() = %q", got)
	}
}

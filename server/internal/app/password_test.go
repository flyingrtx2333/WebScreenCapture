package app

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Fatal("valid password was rejected")
	}
	if VerifyPassword("wrong password", hash) {
		t.Fatal("wrong password was accepted")
	}
}

func TestRejectMalformedPasswordHash(t *testing.T) {
	for _, value := range []string{"", "$argon2id$bad", "$argon2id$v=19$m=999999,t=1,p=1$YWJj$YWJj"} {
		if VerifyPassword("anything", value) {
			t.Fatalf("malformed hash accepted: %q", value)
		}
	}
}

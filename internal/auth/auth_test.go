package auth

import (
	"testing"
	"time"
)

func TestPassword(t *testing.T) {
	h, err := HashPassword("demo1234")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "demo1234") || CheckPassword(h, "demo12345") || CheckPassword("garbage", "demo1234") {
		t.Fatal("password check failed")
	}
}

func TestToken(t *testing.T) {
	iss := NewIssuer("secret", time.Hour)
	tok, _, err := iss.Issue("u1", "CLIENT", "Анна", "c1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := iss.Parse(tok)
	if err != nil || c.Subject != "u1" || c.Role != "CLIENT" {
		t.Fatalf("parse: %v %+v", err, c)
	}
	if _, err := NewIssuer("other", time.Hour).Parse(tok); err == nil {
		t.Fatal("foreign signature accepted")
	}
	expired, _, _ := NewIssuer("secret", -time.Minute).Issue("u1", "CLIENT", "", "")
	if _, err := iss.Parse(expired); err == nil {
		t.Fatal("expired token accepted")
	}
}

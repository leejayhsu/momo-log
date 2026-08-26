package auth

import (
	"net/http/httptest"
	"testing"
)

func TestSessionCookieLastsNinetyDays(t *testing.T) {
	manager := &Manager{}
	response := httptest.NewRecorder()
	manager.SetSessionCookie(response, "token")
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != 90*24*60*60 {
		t.Fatalf("session cookie = %+v", cookies)
	}
}

func TestValidUsername(t *testing.T) {
	for _, value := range []string{"Lee", "Momo's human", "田中"} {
		if !validUsername(value) {
			t.Errorf("validUsername(%q) = false", value)
		}
	}
	for _, value := range []string{"", "bad\nname"} {
		if validUsername(value) {
			t.Errorf("validUsername(%q) = true", value)
		}
	}
}

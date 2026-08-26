package server

import (
	"net/http/httptest"
	"testing"
)

func TestADR0084_GitHubSessionGetsTheCompleteOperatorView(t *testing.T) {
	server := &Server{}
	visibility := server.visibilityFor(httptest.NewRequest("GET", "/", nil))
	if visibility.Restricted() {
		t.Fatal("a browser session received a restricted catalog")
	}
}

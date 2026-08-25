package server

import "testing"

func TestViewerCacheScopeTracksAccessInsteadOfRepositoryOrder(t *testing.T) {
	first := viewerCacheScope("octocat", []string{"example/private", "example/public"}, false)
	reordered := viewerCacheScope("octocat", []string{"example/public", "example/private"}, false)
	if first != reordered {
		t.Fatal("the same access set produced different cache scopes")
	}
	if first == viewerCacheScope("octocat", []string{"example/public"}, false) {
		t.Fatal("different repository access shared a cache scope")
	}
	if first == viewerCacheScope("octocat", []string{"example/private", "example/public"}, true) {
		t.Fatal("observed-entity access shared a cache scope")
	}
}

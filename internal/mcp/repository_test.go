package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

type memoryRepositoryFiles struct {
	file    *write.RepositoryFile
	token   string
	written []byte
}

func (f *memoryRepositoryFiles) RepositoryRoot(context.Context, string) (*write.RepositoryFile, error) {
	return f.file, nil
}

func (f *memoryRepositoryFiles) SetRepositoryRoot(_ context.Context, token, repository string, body []byte) (*write.Result, error) {
	f.token, f.written = token, body
	return &write.Result{
		Ref: repository, Repository: repository, Path: write.RootFile, Created: !f.file.Exists,
		Commit: "c0ffee", URL: "https://github.com/example/homelab/commit/c0ffee",
	}, nil
}

func TestRepositoryToolReadsAndCreatesTheRootFile(t *testing.T) {
	idx := newIndex(t)
	tokens := &proof.Store{}
	files := &memoryRepositoryFiles{file: &write.RepositoryFile{
		Repository: "example/homelab", Path: write.RootFile,
		Template: []byte("---\ndusk: v1alpha1\nnamespace: example\nkind: repository\nname: homelab\n---\n"),
	}}
	session := serve(t, mcp.New(mcp.Options{
		Catalog: idx, Tokens: tokens, Repositories: files, Version: "test",
	}))

	read := call(t, session, "repository", map[string]any{"repository": "example/homelab"})
	if !strings.Contains(read, "has no `dusk.md` yet") || !strings.Contains(read, "kind: repository") {
		t.Fatalf("read = %q, want the missing state and editable starter", read)
	}
	token := tokenFrom(t, read)
	written := string(files.file.Template) + "\nA repository worth remembering.\n"
	answer := call(t, session, "repository", map[string]any{
		"repository": "example/homelab", "dusk_md": written, "proof": token,
	})
	if !strings.Contains(answer, "Created") {
		t.Fatalf("write = %q, want a created dusk.md", answer)
	}
	if files.token != token || string(files.written) != written {
		t.Fatalf("token = %q, body = %q", files.token, files.written)
	}
}

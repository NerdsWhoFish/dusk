package main

import "testing"

// The container entrypoint is the bare binary with no arguments. Without a
// default command that invocation prints help and exits zero, which Kubernetes
// sees as a container that keeps finishing: a crash loop with no error in it.
func TestBareInvocationServes(t *testing.T) {
	root := command()

	if root.DefaultCommand != "serve" {
		t.Fatalf("DefaultCommand = %q, want %q", root.DefaultCommand, "serve")
	}
	for _, sub := range root.Commands {
		if sub.Name == root.DefaultCommand {
			return
		}
	}
	t.Fatalf("DefaultCommand %q is not one of the registered commands", root.DefaultCommand)
}

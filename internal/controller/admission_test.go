package controller_test

import (
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/controller"
)

func TestAdmissionChecksCurrentPermittedInstallationRepositories(t *testing.T) {
	fake := &fakeGitHub{installs: []install{
		{id: 1, account: "acme", repos: map[string]string{"acme/infra": "main"}},
	}}
	ctrl, _ := newController(t, fake, "acme", controller.Options{})
	for _, test := range []struct {
		name     string
		readable []string
		want     bool
	}{
		{"empty", nil, false},
		{"unrelated", []string{"stranger/unrelated"}, false},
		{"same owner only", []string{"acme/not-installed"}, false},
		{"overlap", []string{"stranger/unrelated", "acme/infra"}, true},
		{"case insensitive", []string{"Acme/Infra"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ctrl.SharesRepository(t.Context(), test.readable)
			if err != nil || got != test.want {
				t.Fatalf("SharesRepository = %v, %v; want %v", got, err, test.want)
			}
		})
	}
	fake.installs = nil
	if admitted, err := ctrl.SharesRepository(t.Context(), []string{"acme/infra"}); err != nil || admitted {
		t.Fatalf("revoked installation retained access: %v, %v", admitted, err)
	}
}

func TestAdmissionIgnoresAnUninvitedInstallation(t *testing.T) {
	fake := &fakeGitHub{installs: []install{
		{id: 2, account: "stranger", repos: map[string]string{"stranger/unrelated": "main"}},
	}}
	ctrl, _ := newController(t, fake, "acme", controller.Options{})
	admitted, err := ctrl.SharesRepository(t.Context(), []string{"stranger/unrelated"})
	if err != nil || admitted {
		t.Fatalf("uninvited installation granted admission: %v, %v", admitted, err)
	}
}

func TestAdmissionRefusesAnIncompleteInstallationRead(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{id: 1, account: "acme", listFails: true}}}
	ctrl, _ := newController(t, fake, "acme", controller.Options{})
	admitted, err := ctrl.SharesRepository(t.Context(), []string{"acme/infra"})
	if err == nil || admitted {
		t.Fatalf("failed repository discovery granted admission: %v, %v", admitted, err)
	}
}

package controller

import (
	"context"
	"strings"

	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
)

// SharesRepository checks current installation grants, independent of OAuth
// token type and of the catalog's last reconciliation.
func (c *Controller) SharesRepository(ctx context.Context, readable []string) (bool, error) {
	if len(readable) == 0 {
		return false, nil
	}
	tokens, owner, err := c.auth(ctx)
	if err != nil {
		return false, err
	}
	installations, err := c.opts.Client.Installations(ctx, tokens.App)
	if err != nil {
		return false, err
	}
	wanted := make(map[string]bool, len(readable))
	for _, repository := range readable {
		wanted[strings.ToLower(repository)] = true
	}
	for _, installation := range installations {
		if !c.Permitted(installation.Account.Login, owner) {
			continue
		}
		install := &githubapp.Install{Client: c.opts.Client, Tokens: tokens, ID: installation.ID}
		repositories, err := install.Repositories(ctx)
		if err != nil {
			return false, err
		}
		for _, repository := range repositories {
			if wanted[strings.ToLower(repository.Slug())] {
				return true, nil
			}
		}
	}
	return false, nil
}

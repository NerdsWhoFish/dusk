package write

import (
	"context"
	"errors"
	"fmt"

	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// Home returns the portal page the config repository declares, or
// fs.ErrNotExist when it declares none. It reads through the write path, so
// the UI renders what is committed rather than what a reconcile last indexed.
func (w *Writer) Home(ctx context.Context) ([]byte, error) {
	return w.configFile(ctx, page.Path)
}

// SetHome writes the portal page, which is how an agent curates the layout.
// It parses before committing: a page that cannot be read renders as nothing,
// and learning that from a blank homepage is worse than being refused.
func (w *Writer) SetHome(ctx context.Context, token string, body []byte) (*Result, error) {
	if w.ConfigRepository == "" {
		return nil, errors.New("write: no config repository is set, so there is nowhere to keep a page")
	}
	if _, _, err := page.Parse(body); err != nil {
		return nil, fmt.Errorf("write: that page would not render: %w", err)
	}
	return w.setConfigFile(ctx, token, page.Path, "page: update "+page.Path,
		body, proof.Portal(page.Path))
}

package write

import (
	"context"
	"errors"
	"fmt"

	"github.com/NerdsWhoFish/dusk/internal/contextconfig"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// Context returns the operator-owned agent context profile as committed.
func (w *Writer) Context(ctx context.Context) ([]byte, error) {
	return w.configFile(ctx, contextconfig.Path)
}

// SetContext replaces the agent context profile after validating the complete
// file. A malformed profile would fail every future dusk_context call.
func (w *Writer) SetContext(ctx context.Context, token string, body []byte) (*Result, error) {
	if w.ConfigRepository == "" {
		return nil, errors.New("write: no config repository is set, so there is nowhere to keep the agent context profile")
	}
	if _, err := contextconfig.Parse(body); err != nil {
		return nil, fmt.Errorf("write: that context profile would not render: %w", err)
	}
	return w.setConfigFile(ctx, token, contextconfig.Path,
		"context: update "+contextconfig.Path, body, proof.ContextProfile(contextconfig.Path))
}

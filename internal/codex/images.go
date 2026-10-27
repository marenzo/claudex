package codex

import (
	"context"
	"net/http"

	"github.com/marenzo/claudex/internal/auth"
)

// Images uses the same subscription endpoints as Codex's native image_gen tool.
// Only an authentication rejection is retried; ambiguous generation failures
// must not automatically spend the user's image allowance a second time.
func (c *Client) Images(ctx context.Context, credential auth.Credential, body []byte, turnID string, edit bool) (*http.Response, auth.Credential, error) {
	path := "/codex/images/generations"
	if edit {
		path = "/codex/images/edits"
	}
	return c.withRefresh(ctx, credential, http.MethodPost, path, body, turnID)
}

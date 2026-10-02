package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/bundle"
	"github.com/vessica-labs/vessica-studio/internal/library"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// EnsureBundle is idempotent and separate from canonical presentation transport.
func (c *Client) EnsureBundle(ctx context.Context, workspace string, a library.BundleAsset, data []byte) error {
	if err := bundle.Verify(data, a); err != nil {
		return err
	}
	if err := c.negotiate(ctx, "asset.bundle.write"); err != nil {
		return err
	}
	path := "/v1/workspaces/" + url.PathEscape(workspace) + "/bundles/" + a.Hash + "?bytes=" + strconv.FormatInt(a.Bytes, 10) + "&entrypoint=" + url.QueryEscape(a.Entrypoint)
	var receipt struct {
		Present bool `json:"present"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &receipt, true); err != nil {
		return err
	}
	if receipt.Present {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint.String()+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/vnd.vstd.bundle+zip")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Vstd-Protocol", ProtocolVersion)
	req.Header.Set("X-Vstd-Version", c.clientVersion)
	if c.token == nil {
		return fmt.Errorf("Cloud authentication is required")
	}
	token, err := c.token.Token(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Kind: ErrorOffline, Cause: err}
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(response) > 4096 {
		return fmt.Errorf("invalid bundle receipt")
	}
	if resp.StatusCode != http.StatusOK {
		return c.remoteError(resp, response)
	}
	var stored struct {
		Stored bool `json:"stored"`
	}
	if json.Unmarshal(response, &stored) != nil || !stored.Stored {
		return fmt.Errorf("invalid bundle receipt")
	}
	return nil
}

// Package cloudpublish selects synchronized revisions and orchestrates publication
// through the versioned cloud boundary. It does not make publication-policy
// decisions or inspect local content.
package cloudpublish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
)

var (
	ErrRevisionRequired   = errors.New("an explicit synchronized revision is required while local changes or a conflict are pending")
	ErrInvalidAssociation = errors.New("cloud workspace association is invalid")
	ErrInvalidPublication = errors.New("cloud returned an invalid publication")
)

// Association is the read-only publication view of a connected workspace.
// Workspace synchronization owns and supplies these values.
type Association struct {
	WorkspaceID            string
	SynchronizedRevisionID string
	Unsynced               bool
	Conflict               bool
}

// Service is the narrow portion of the versioned cloud client used for
// publication. cloud.Client satisfies this interface.
type Service interface {
	Capabilities(context.Context) (cloud.Capabilities, error)
	Publish(context.Context, string, cloud.PublicationRequest) (cloud.Publication, error)
	Publication(context.Context, string, string) (cloud.Publication, error)
}

type Client struct {
	service       Service
	clientVersion string
}

func New(service Service, clientVersion string) *Client {
	return &Client{service: service, clientVersion: clientVersion}
}

// Publish publishes explicitRevision, or the association's synchronized
// revision when local state is clean and unambiguous.
func (c *Client) Publish(ctx context.Context, association Association, explicitRevision string) (cloud.Publication, error) {
	if err := validateAssociation(association); err != nil {
		return cloud.Publication{}, err
	}
	revision := strings.TrimSpace(explicitRevision)
	if revision == "" {
		if association.Unsynced || association.Conflict || strings.TrimSpace(association.SynchronizedRevisionID) == "" {
			return cloud.Publication{}, ErrRevisionRequired
		}
		revision = association.SynchronizedRevisionID
	}
	if err := c.negotiate(ctx, cloud.CapabilityPublicationWrite); err != nil {
		return cloud.Publication{}, err
	}
	operationID := stableOperationID(association.WorkspaceID, revision)
	publication, err := c.service.Publish(ctx, association.WorkspaceID, cloud.PublicationRequest{
		RevisionID: revision, OperationID: operationID,
	})
	if err != nil {
		if !ambiguous(err) {
			return cloud.Publication{}, err
		}
		var lookupErr error
		publication, lookupErr = c.service.Publication(ctx, association.WorkspaceID, operationID)
		if lookupErr != nil {
			return cloud.Publication{}, err
		}
	}
	if err := validatePublication(publication, association.WorkspaceID, revision); err != nil {
		return cloud.Publication{}, err
	}
	return publication, nil
}

func (c *Client) Status(ctx context.Context, workspaceID, publicationID string) (cloud.Publication, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(publicationID) == "" {
		return cloud.Publication{}, ErrInvalidAssociation
	}
	if err := c.negotiate(ctx, cloud.CapabilityPublicationRead); err != nil {
		return cloud.Publication{}, err
	}
	publication, err := c.service.Publication(ctx, workspaceID, publicationID)
	if err != nil {
		return cloud.Publication{}, err
	}
	if err := validatePublication(publication, workspaceID, ""); err != nil {
		return cloud.Publication{}, err
	}
	return publication, nil
}

func (c *Client) negotiate(ctx context.Context, capability string) error {
	if c == nil || c.service == nil || strings.TrimSpace(c.clientVersion) == "" {
		return fmt.Errorf("cloud publication client is not configured")
	}
	caps, err := c.service.Capabilities(ctx)
	if err != nil {
		return err
	}
	if caps.Protocol != cloud.ProtocolVersion || versionLess(c.clientVersion, caps.MinimumClientVersion) {
		return &cloud.IncompatibleError{Protocol: caps.Protocol, MinimumClientVersion: caps.MinimumClientVersion}
	}
	for _, available := range caps.Capabilities {
		if available == capability {
			return nil
		}
	}
	return &cloud.IncompatibleError{Protocol: caps.Protocol, MissingCapability: capability}
}

func validateAssociation(a Association) error {
	if strings.TrimSpace(a.WorkspaceID) == "" {
		return ErrInvalidAssociation
	}
	return nil
}

func stableOperationID(workspace, revision string) string {
	sum := sha256.Sum256([]byte("publication\x00" + workspace + "\x00" + revision))
	return "publish-" + hex.EncodeToString(sum[:16])
}

func ambiguous(err error) bool {
	return cloud.IsKind(err, cloud.ErrorOffline) || cloud.IsKind(err, cloud.ErrorRateLimit) || cloud.IsKind(err, cloud.ErrorRemote)
}

func validatePublication(p cloud.Publication, workspace, revision string) error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.RevisionID) == "" || strings.TrimSpace(p.Status) == "" {
		return ErrInvalidPublication
	}
	if p.WorkspaceID != "" && p.WorkspaceID != workspace {
		return ErrInvalidPublication
	}
	if revision != "" && p.RevisionID != revision {
		return ErrInvalidPublication
	}
	if p.URL != "" {
		u, err := url.Parse(p.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return ErrInvalidPublication
		}
	}
	return nil
}

func versionLess(current, minimum string) bool {
	if minimum == "" {
		return false
	}
	var c, m [3]int
	if _, err := fmt.Sscanf(strings.TrimPrefix(current, "v"), "%d.%d.%d", &c[0], &c[1], &c[2]); err != nil {
		return true
	}
	if _, err := fmt.Sscanf(strings.TrimPrefix(minimum, "v"), "%d.%d.%d", &m[0], &m[1], &m[2]); err != nil {
		return true
	}
	for i := range c {
		if c[i] != m[i] {
			return c[i] < m[i]
		}
	}
	return false
}

// Package cloud implements the public, versioned Vessica Studio Cloud HTTP boundary.
package cloud

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

const ProtocolVersion = "1"

const (
	CapabilityWorkspaceRead    = "workspace.read"
	CapabilityWorkspaceSync    = "workspace.sync"
	CapabilityPublicationRead  = "publication.read"
	CapabilityPublicationWrite = "publication.write"
)

type Capabilities struct {
	Protocol             string   `json:"protocol"`
	MinimumClientVersion string   `json:"minimum_client_version,omitempty"`
	Capabilities         []string `json:"capabilities"`
}
type DeviceAuthorizationRequest struct {
	ClientVersion string `json:"client_version,omitempty"`
}
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}
type TokenRequest struct {
	DeviceCode   string `json:"device_code,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}
type RevokeRequest struct {
	RefreshToken string `json:"refresh_token"`
}
type Account struct {
	ID          string `json:"id"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}
type Workspace struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	HeadRevisionID string `json:"head_revision_id,omitempty"`
}
type WorkspaceList struct {
	Workspaces []Workspace `json:"workspaces"`
	NextCursor string      `json:"next_cursor,omitempty"`
}
type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Mode    uint32 `json:"mode,omitempty"`
}
type Revision struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"`
	Author      Account   `json:"author,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	Files       []File    `json:"files,omitempty"`
}
type SyncRequest struct {
	BaseRevisionID string `json:"base_revision_id"`
	Files          []File `json:"files"`
	Message        string `json:"message,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
}
type PublicationRequest struct {
	RevisionID  string `json:"revision_id"`
	OperationID string `json:"operation_id,omitempty"`
}
type Publication struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	RevisionID  string    `json:"revision_id"`
	Status      string    `json:"status"`
	URL         string    `json:"url,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type ErrorKind string

const (
	ErrorOffline           ErrorKind = "offline"
	ErrorAuth              ErrorKind = "auth"
	ErrorForbidden         ErrorKind = "forbidden"
	ErrorConflict          ErrorKind = "conflict"
	ErrorIncompatible      ErrorKind = "incompatible"
	ErrorRateLimit         ErrorKind = "rate_limit"
	ErrorMalformedResponse ErrorKind = "malformed_response"
	ErrorRemote            ErrorKind = "remote"
)

type Error struct {
	Kind       ErrorKind
	StatusCode int
	Code       string
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("cloud request failed (%s: %s)", e.Kind, e.Code)
	}
	return fmt.Sprintf("cloud request failed (%s)", e.Kind)
}
func (e *Error) Unwrap() error { return e.Cause }
func IsKind(err error, kind ErrorKind) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == kind
}

type IncompatibleError struct {
	Protocol             string
	MinimumClientVersion string
	MissingCapability    string
}

func (e *IncompatibleError) Error() string {
	if e.MinimumClientVersion != "" {
		return "cloud protocol is incompatible; upgrade vstd to at least " + e.MinimumClientVersion
	}
	if e.MissingCapability != "" {
		return "cloud endpoint does not support required capability " + e.MissingCapability
	}
	return "cloud protocol is incompatible"
}

type ConflictError struct {
	Code                string
	CloudHeadRevisionID string
}

func (e *ConflictError) Error() string {
	return "cloud workspace changed since the local base; sync or pull before retrying"
}

type wireError struct {
	Code                 string `json:"code"`
	Message              string `json:"message"`
	MinimumClientVersion string `json:"minimum_client_version"`
	Protocol             string `json:"protocol"`
	CloudHeadRevisionID  string `json:"cloud_head_revision_id"`
}

func statusKind(status int) ErrorKind {
	switch status {
	case http.StatusUnauthorized:
		return ErrorAuth
	case http.StatusForbidden:
		return ErrorForbidden
	case http.StatusConflict:
		return ErrorConflict
	case http.StatusTooManyRequests:
		return ErrorRateLimit
	default:
		return ErrorRemote
	}
}

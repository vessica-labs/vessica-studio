package cloudpublish

import (
	"context"
	"errors"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
)

type fakeService struct {
	caps         cloud.Capabilities
	published    cloud.Publication
	publishErr   error
	received     cloud.PublicationRequest
	statusID     string
	publishCalls int
}

func (f *fakeService) Capabilities(context.Context) (cloud.Capabilities, error) { return f.caps, nil }
func (f *fakeService) Publish(_ context.Context, _ string, in cloud.PublicationRequest) (cloud.Publication, error) {
	f.publishCalls++
	f.received = in
	return f.published, f.publishErr
}
func (f *fakeService) Publication(_ context.Context, _, id string) (cloud.Publication, error) {
	f.statusID = id
	return f.published, nil
}

func TestPublishSelectsSynchronizedRevisionAndStableOperation(t *testing.T) {
	f := &fakeService{caps: publishCaps(), published: cloud.Publication{ID: "pub-1", WorkspaceID: "ws-1", RevisionID: "rev-1", Status: "queued"}}
	p := New(f, "1.2.0")
	a := Association{WorkspaceID: "ws-1", SynchronizedRevisionID: "rev-1"}

	got, err := p.Publish(context.Background(), a, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "pub-1" || f.received.RevisionID != "rev-1" {
		t.Fatalf("unexpected publication: %#v request %#v", got, f.received)
	}
	first := f.received.OperationID
	if first == "" {
		t.Fatal("missing operation ID")
	}
	_, err = p.Publish(context.Background(), a, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.received.OperationID != first {
		t.Fatalf("operation ID changed: %q != %q", f.received.OperationID, first)
	}
}

func TestRevisionRequiresExplicitSelectionWhenLocalStateIsNotPublishable(t *testing.T) {
	for _, a := range []Association{
		{WorkspaceID: "ws", SynchronizedRevisionID: "rev", Unsynced: true},
		{WorkspaceID: "ws", SynchronizedRevisionID: "rev", Conflict: true},
		{WorkspaceID: "ws"},
	} {
		f := &fakeService{caps: publishCaps()}
		_, err := New(f, "1.2.0").Publish(context.Background(), a, "")
		if !errors.Is(err, ErrRevisionRequired) {
			t.Fatalf("association %#v: %v", a, err)
		}
		if f.publishCalls != 0 {
			t.Fatal("publish called")
		}
	}
}

func TestPublishExplicitRevisionDoesNotInferDirtyLocalContent(t *testing.T) {
	f := &fakeService{caps: publishCaps(), published: cloud.Publication{ID: "p", RevisionID: "chosen", Status: "ready"}}
	_, err := New(f, "1.2.0").Publish(context.Background(), Association{WorkspaceID: "ws", Unsynced: true}, "chosen")
	if err != nil {
		t.Fatal(err)
	}
	if f.received.RevisionID != "chosen" {
		t.Fatalf("revision = %q", f.received.RevisionID)
	}
}

func TestPublishProtocolFailurePreventsMutation(t *testing.T) {
	f := &fakeService{caps: cloud.Capabilities{Protocol: cloud.ProtocolVersion, MinimumClientVersion: "9.0.0", Capabilities: []string{cloud.CapabilityPublicationWrite}}}
	_, err := New(f, "1.2.0").Publish(context.Background(), Association{WorkspaceID: "ws"}, "rev")
	var incompatible *cloud.IncompatibleError
	if !errors.As(err, &incompatible) || incompatible.MinimumClientVersion != "9.0.0" {
		t.Fatalf("error = %v", err)
	}
	if f.publishCalls != 0 {
		t.Fatal("publish called after incompatible negotiation")
	}
}

func TestPublishResolvesAmbiguousOutcomeByOperationStatus(t *testing.T) {
	f := &fakeService{caps: publishCaps(), publishErr: &cloud.Error{Kind: cloud.ErrorOffline}, published: cloud.Publication{ID: "p", RevisionID: "rev", Status: "queued"}}
	got, err := New(f, "1.2.0").Publish(context.Background(), Association{WorkspaceID: "ws"}, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if f.statusID == "" || f.statusID != f.received.OperationID || got.ID != "p" {
		t.Fatalf("lookup %q request %#v publication %#v", f.statusID, f.received, got)
	}
}

func TestStatusNegotiatesAndPreservesServiceState(t *testing.T) {
	f := &fakeService{caps: publishCaps(), published: cloud.Publication{ID: "p", WorkspaceID: "ws", RevisionID: "rev", Status: "service-specific", URL: "https://example.test/p"}}
	got, err := New(f, "1.2.0").Status(context.Background(), "ws", "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "service-specific" || got.URL != "https://example.test/p" {
		t.Fatalf("publication = %#v", got)
	}
}

func publishCaps() cloud.Capabilities {
	return cloud.Capabilities{Protocol: cloud.ProtocolVersion, Capabilities: []string{cloud.CapabilityPublicationRead, cloud.CapabilityPublicationWrite}}
}

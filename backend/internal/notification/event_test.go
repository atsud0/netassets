package notification

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type recordingEventExecer struct {
	query string
	args  []any
	err   error
}

func (r *recordingEventExecer) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	r.query = query
	r.args = args
	return pgconn.NewCommandTag("INSERT 0 1"), r.err
}

func TestWriteEventRejectsIncompleteInput(t *testing.T) {
	execer := &recordingEventExecer{}
	err := WriteEvent(context.Background(), execer, EventInput{
		EventType:            EventAssetDeleted,
		Category:             CategoryAssetChange,
		TargetOrganizationID: "11111111-1111-1111-1111-111111111111",
		ActorUserID:          "22222222-2222-2222-2222-222222222222",
		ResourceType:         "asset",
	})
	if err == nil {
		t.Fatal("WriteEvent() error = nil, want invalid input error")
	}
	if execer.query != "" {
		t.Fatal("invalid event must not execute SQL")
	}
}

func TestWriteEventUsesDurableBatchAggregation(t *testing.T) {
	execer := &recordingEventExecer{}
	err := WriteEvent(context.Background(), execer, EventInput{
		EventType:            EventAssetNetworkAreaChanged,
		Category:             CategoryAssetChange,
		TargetOrganizationID: "11111111-1111-1111-1111-111111111111",
		TargetUserID:         "44444444-4444-4444-4444-444444444444",
		ActorUserID:          "22222222-2222-2222-2222-222222222222",
		RequiredPermission:   "asset.manage",
		ResourceType:         "asset",
		ResourceID:           "33333333-3333-3333-3333-333333333333",
		ResourceLabel:        "example.com",
		BatchKey:             "request-123",
		Details:              map[string]any{"after": "办事处网络"},
	})
	if err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	for _, fragment := range []string{
		"ON CONFLICT (event_type, target_organization_id, target_user_id, batch_key)",
		"item_count = notification_events.item_count + 1",
		"resource_id = NULL",
	} {
		if !strings.Contains(execer.query, fragment) {
			t.Fatalf("event SQL does not contain %q", fragment)
		}
	}
	if got, want := len(execer.args), 12; got != want {
		t.Fatalf("event SQL argument count = %d, want %d", got, want)
	}
	if got := execer.args[3]; got != "44444444-4444-4444-4444-444444444444" {
		t.Fatalf("target user argument = %v, want target user ID", got)
	}
	if got := execer.args[9]; got != "request-123" {
		t.Fatalf("batch key argument = %v, want request-123", got)
	}
	if got := execer.args[10]; got != "info" {
		t.Fatalf("default level argument = %v, want info", got)
	}
}

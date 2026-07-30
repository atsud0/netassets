package exportjob

import (
	"encoding/json"
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestValidID(t *testing.T) {
	valid := "123e4567-e89b-12d3-a456-426614174000"
	for _, test := range []struct {
		value string
		want  bool
	}{
		{valid, true},
		{"../" + valid, false},
		{"123e4567-e89b-12d3-a456-42661417400z", false},
		{"123e4567e89b12d3a456426614174000", false},
		{"", false},
	} {
		if got := validID(test.value); got != test.want {
			t.Fatalf("validID(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestDecodeStrictRejectsUnknownFilterField(t *testing.T) {
	var filter AssetFilter
	if err := decodeStrict([]byte(`{"status":"active","unexpected":true}`), &filter); err == nil {
		t.Fatal("decodeStrict accepted an unknown field")
	}
	if err := decodeStrict([]byte(`{"status":"active"}`), &filter); err != nil {
		t.Fatalf("decodeStrict rejected a supported filter: %v", err)
	}
	if err := decodeStrict([]byte(`{"status":"active"} {}`), &filter); err == nil {
		t.Fatal("decodeStrict accepted multiple JSON documents")
	}
}

func TestPrincipalSnapshotExcludesAuthenticationSecrets(t *testing.T) {
	principal := auth.Principal{
		UserID: "user-id", Username: "admin", DisplayName: "管理员",
		Roles: []string{"group_admin"}, Permissions: []string{"asset.manage"},
		SessionID: "session-secret", TokenHash: "token-secret", APITokenID: "api-token-id",
		AuthenticationType: "password",
	}
	encoded, err := json.Marshal(snapshotPrincipal(principal))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"session-secret", "token-secret", "api-token-id", "authentication_type"} {
		if strings.Contains(text, secret) {
			t.Fatalf("principal snapshot leaked %q: %s", secret, text)
		}
	}
	var snapshot PrincipalSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Principal().UserID != principal.UserID || !snapshot.Principal().HasPermission("asset.manage") {
		t.Fatalf("principal snapshot did not preserve authorization data: %#v", snapshot)
	}
}

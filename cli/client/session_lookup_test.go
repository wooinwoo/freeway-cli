package client

import (
	"path/filepath"
	"testing"
)

func TestSessionScopeAndUnambiguousIdentity(t *testing.T) {
	root := t.TempDir()
	records := []ConversationSummary{{ID: "1234-aaaa", Title: "Build tooling", Workspace: root}, {ID: "1234-bbbb", Title: "Build tooling", Workspace: filepath.Join(root, "other")}, {ID: "9876-cccc", Title: "Legacy chat"}}
	if got := ScopeConversations(records, root, false); len(got) != 2 || got[0].ID != "1234-aaaa" || got[1].ID != "9876-cccc" {
		t.Fatalf("workspace leaked: %#v", got)
	}
	if len(ScopeConversations(records, root, true)) != 3 {
		t.Fatal("--all did not include other projects")
	}
	if got := FindConversations(records, "1234-aaaa"); len(got) != 1 || got[0].ID != "1234-aaaa" {
		t.Fatal("exact ID was ambiguous")
	}
	if len(FindConversations(records, "1234")) != 2 {
		t.Fatal("ambiguous prefix was silently resolved")
	}
	if len(FindConversations(records, "legacy chat")) != 1 {
		t.Fatal("case-insensitive title lookup failed")
	}
}

package commands

import "testing"

func TestListCmdHasLsAlias(t *testing.T) {
	if got := NewListCmd().Aliases; len(got) != 1 || got[0] != "ls" {
		t.Fatalf("expected list command alias [ls], got %v", got)
	}
}

func TestRemoveCmdHasRmAlias(t *testing.T) {
	if got := NewRemoveCmd().Aliases; len(got) != 1 || got[0] != "rm" {
		t.Fatalf("expected remove command alias [rm], got %v", got)
	}
}

func TestLabelListCmdHasLsAlias(t *testing.T) {
	if got := newLabelListCmd().Aliases; len(got) != 1 || got[0] != "ls" {
		t.Fatalf("expected label list command alias [ls], got %v", got)
	}
}

// TestLabelRemoveCmdHasRmAlias goes through the parent group because the write
// verbs share one constructor: the alias lives in the spec passed to it, so
// asserting on a locally built command would only re-state the test's own
// literal rather than what "torrent label" actually registers.
func TestLabelRemoveCmdHasRmAlias(t *testing.T) {
	remove, _, err := NewLabelCmd().Find([]string{"remove"})
	if err != nil {
		t.Fatalf("expected a label remove command: %v", err)
	}

	if got := remove.Aliases; len(got) != 1 || got[0] != "rm" {
		t.Fatalf("expected label remove command alias [rm], got %v", got)
	}
}

func TestMoveCmdHasMvAlias(t *testing.T) {
	if got := NewMoveCmd().Aliases; len(got) != 1 || got[0] != "mv" {
		t.Fatalf("expected move command alias [mv], got %v", got)
	}
}

func TestStopCmdHasPauseAlias(t *testing.T) {
	if got := NewStopCmd().Aliases; len(got) != 1 || got[0] != "pause" {
		t.Fatalf("expected stop command alias [pause], got %v", got)
	}
}

func TestShowCmdHasInfoAlias(t *testing.T) {
	if got := NewShowCmd().Aliases; len(got) != 1 || got[0] != "info" {
		t.Fatalf("expected show command alias [info], got %v", got)
	}
}

func TestServerListCmdHasLsAlias(t *testing.T) {
	if got := newServerListCmd().Aliases; len(got) != 1 || got[0] != "ls" {
		t.Fatalf("expected server list command alias [ls], got %v", got)
	}
}

func TestServerRemoveCmdHasRmAlias(t *testing.T) {
	if got := newServerRemoveCmd().Aliases; len(got) != 1 || got[0] != "rm" {
		t.Fatalf("expected server remove command alias [rm], got %v", got)
	}
}

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

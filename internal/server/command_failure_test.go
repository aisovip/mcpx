package server

import "testing"

func TestCommandFailureCodeUsesExitStatus(t *testing.T) {
	if got := commandFailureCode(127); got != "COMMAND_NOT_FOUND" {
		t.Fatalf("commandFailureCode(127) = %q", got)
	}
	if got := commandFailureCode(126); got != "PROCESS_EXIT" {
		t.Fatalf("commandFailureCode(126) = %q", got)
	}
}

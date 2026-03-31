package host

import "testing"

func TestBuildRemoteCommandQuotesArguments(t *testing.T) {
	got := buildRemoteCommand("bash", "-lc", "echo hello world")
	want := "bash -lc 'echo hello world'"
	if got != want {
		t.Fatalf("buildRemoteCommand() = %q, want %q", got, want)
	}
}

func TestShellQuoteLeavesSimpleValuesAlone(t *testing.T) {
	if got := shellQuote("ubuntu"); got != "ubuntu" {
		t.Fatalf("shellQuote() = %q, want ubuntu", got)
	}
}

func TestClassifySSHError(t *testing.T) {
	err := classifySSHError("Permission denied (publickey).", dummyError("ssh exit status 255"))
	if err == nil || err.Error() == "" {
		t.Fatal("classifySSHError() returned empty error")
	}
}

type dummyError string

func (d dummyError) Error() string { return string(d) }

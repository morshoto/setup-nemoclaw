package prompt

import (
	"bytes"
	"strings"
	"testing"
)

func TestSessionSelectUsesDefaultInNonInteractiveMode(t *testing.T) {
	session := NewSession(strings.NewReader(""), &bytes.Buffer{})
	session.Interactive = false

	got, err := session.Select("platform", []string{"aws", "gcp"}, "aws")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got != "aws" {
		t.Fatalf("Select() = %q, want aws", got)
	}
}

func TestSessionConfirmAndTextAndInt(t *testing.T) {
	in := strings.NewReader("y\nhello\n42\n")
	out := &bytes.Buffer{}
	session := NewSession(in, out)

	confirmed, err := session.Confirm("Continue", false)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !confirmed {
		t.Fatal("Confirm() = false, want true")
	}

	text, err := session.Text("Name", "")
	if err != nil {
		t.Fatalf("Text() error = %v", err)
	}
	if text != "hello" {
		t.Fatalf("Text() = %q, want hello", text)
	}

	value, err := session.Int("Count", 0)
	if err != nil {
		t.Fatalf("Int() error = %v", err)
	}
	if value != 42 {
		t.Fatalf("Int() = %d, want 42", value)
	}

	if out.Len() == 0 {
		t.Fatal("expected prompts to be written")
	}
}

package ui

import (
	"bytes"
	"testing"
)

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{Out: &buf, JSON: true}
	w.Print(map[string]string{"a": "1"})
	got := buf.String()
	if got != `{"a":"1"}`+"\n" {
		t.Fatalf("unexpected json: %q", got)
	}
}

func TestPrintHuman(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{Out: &buf}
	w.Println("hi")
	if buf.String() != "hi\n" {
		t.Fatalf("got %q", buf.String())
	}
}

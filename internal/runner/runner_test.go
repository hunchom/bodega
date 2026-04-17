package runner

import (
	"context"
	"testing"
)

func TestFakeRecords(t *testing.T) {
	f := &Fake{Stdout: map[string]string{"echo hi": "hi\n"}}
	r, err := f.Run(context.Background(), "echo", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Stdout) != "hi\n" {
		t.Fatalf("got %q", r.Stdout)
	}
	if len(f.Calls) != 1 || f.Calls[0].Name != "echo" {
		t.Fatalf("calls=%+v", f.Calls)
	}
}

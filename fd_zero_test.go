package wlturbo

import "testing"

func TestOwnedFDZero(t *testing.T) {
	fd := &OwnedFD{fd: 0}
	n, e := fd.Take()
	if e != nil || n != 0 {
		t.Fatalf("Take fd zero = %d %v", n, e)
	}
	if e := fd.Close(); e != nil {
		t.Fatal(e)
	}
}

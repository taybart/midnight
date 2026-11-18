package lua

import (
	"strings"
	"testing"
)

func TestMarkupMorphJS(t *testing.T) {
	L := NewState()
	defer L.Close()
	if err := L.DoString(`return require('` + MarkupMorphLibName + `')`); err != nil {
		t.Fatal(err)
	}
	want := L.ToString(-1)
	js := MarkupMorphJS()
	if js != want {
		t.Fatalf("MarkupMorphJS doesn't match the module (%d vs %d bytes)", len(js), len(want))
	}
	if !strings.HasPrefix(js, "var Idiomorph=") {
		t.Fatalf("MarkupMorphJS starts with %q", js[:min(len(js), 20)])
	}
}

func TestMarkupMountedJS(t *testing.T) {
	// the client sets window.midnight.rpc, which MarkupMountedJS checks
	if !strings.Contains(MarkupLibSource, "window.midnight = {\n    rpc(") {
		t.Fatal("the markup client no longer sets window.midnight.rpc, update MarkupMountedJS")
	}
}

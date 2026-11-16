package luagen

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/parse"
)

func TestGenerateWithComments(t *testing.T) {
	src := "-- a normal comment (dropped)\n" +
		"--- @class Action\n" +
		"--- @field command string\n" +
		"local class Action {\n" +
		"  new(config) {\n" +
		"    self.config = config\n" +
		"  }\n" +
		"}\n"

	chunk, err := parse.ParseMidnight(strings.NewReader(src), "<test>")
	if err != nil {
		t.Fatal(err)
	}
	comments, err := parse.MidnightDocComments(src, "<test>")
	if err != nil {
		t.Fatal(err)
	}

	got := GenerateWithComments(chunk, comments)

	// The doc comments are emitted, adjacent to the declaration they annotate.
	if !strings.Contains(got, "--- @class Action\n--- @field command string\nlocal Action = {}") {
		t.Errorf("doc comments not adjacent to declaration:\n%s", got)
	}
	// A normal comment is still dropped.
	if strings.Contains(got, "a normal comment") {
		t.Errorf("normal comment should be dropped:\n%s", got)
	}

	// Without comments, the behavior is unchanged (comments are dropped).
	plain := Generate(chunk)
	if strings.Contains(plain, "@class Action") {
		t.Errorf("Generate should not emit comments:\n%s", plain)
	}
}

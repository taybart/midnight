package parse

import "testing"

func TestMidnightDocComments(t *testing.T) {
	src := "--- @class Action\n" +
		"--- @field command string\n" +
		"--- @return string\n" +
		"--- a plain doc line\n" +
		"---\n" +
		"-- a normal comment\n" +
		"---- a separator\n" +
		"--[[ a block comment ]]\n" +
		"local class Action {}\n"

	comments, err := MidnightDocComments(src, "<test>")
	if err != nil {
		t.Fatal(err)
	}

	want := map[int]string{
		1: "--- @class Action",
		2: "--- @field command string",
		3: "--- @return string",
		4: "--- a plain doc line",
		5: "---",
	}
	if len(comments) != len(want) {
		t.Fatalf("got %d comments, want %d:\n%v", len(comments), len(want), comments)
	}
	for line, text := range want {
		if comments[line] != text {
			t.Errorf("line %d: got %q, want %q", line, comments[line], text)
		}
	}
	// Non doc comments (normal, separator, block) must be dropped.
	for line := 6; line <= 8; line++ {
		if _, ok := comments[line]; ok {
			t.Errorf("line %d should not be a doc comment", line)
		}
	}
}

package console

import "testing"

func TestRingBufferOrder(t *testing.T) {
	buffer := NewRingBuffer(3)
	buffer.Add("one")
	buffer.Add("two")
	buffer.Add("three")
	buffer.Add("four")

	lines := buffer.GetLines()
	expected := []string{"two", "three", "four"}
	if len(lines) != len(expected) {
		t.Fatalf("unexpected buffer length: %d", len(lines))
	}
	for i, line := range expected {
		if lines[i] != line {
			t.Fatalf("expected %s at %d, got %s", line, i, lines[i])
		}
	}

	last := buffer.GetLast(2)
	if len(last) != 2 || last[0] != "three" || last[1] != "four" {
		t.Fatalf("unexpected last lines: %v", last)
	}
}

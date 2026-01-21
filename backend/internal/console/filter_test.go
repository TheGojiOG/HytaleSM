package console

import "testing"

func TestOutputFilterSearch(t *testing.T) {
	filter, err := NewOutputFilter("search", "hello", false)
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	result := filter.Filter("Hello world")
	if !result.Include {
		t.Fatalf("expected line to be included")
	}

	result = filter.Filter("goodbye")
	if result.Include {
		t.Fatalf("expected line to be excluded")
	}
}

func TestOutputFilterErrors(t *testing.T) {
	filter, err := NewOutputFilter("errors", "", false)
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	result := filter.Filter("ERROR something failed")
	if !result.Include {
		t.Fatalf("expected error line to be included")
	}

	result = filter.Filter("all good")
	if result.Include {
		t.Fatalf("expected non-error line to be excluded")
	}
}

func TestOutputFilterRegex(t *testing.T) {
	filter, err := NewOutputFilter("regex", "h.llo", false)
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	result := filter.Filter("hello")
	if !result.Include {
		t.Fatalf("expected regex match to include line")
	}
}

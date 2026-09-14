package internal

import "testing"

func TestErrLinkedService(t *testing.T) {
	// The bash datastore plugins emit this exact string, and downstream plugin
	// test suites assert on it, so the capitalization is load bearing.
	expected := "Cannot delete linked service"
	if ErrLinkedService.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, ErrLinkedService)
	}
}

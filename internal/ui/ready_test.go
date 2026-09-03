package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A key that arrives before the first frame must not act. Enter now opens an
// SSH connection, so input delivered at startup — a buffered newline from the
// launching shell, say — would connect to whatever happened to be selected.
func TestKeysBeforeFirstFrameAreIgnored(t *testing.T) {
	m := Model{}

	if m.ready {
		t.Fatal("a fresh model should not be ready")
	}
	got, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("enter before the first frame produced a command: %T", cmd)
	}
	if got.(Model).mode != modeBrowse {
		t.Errorf("mode changed to %v", got.(Model).mode)
	}

	// After a size message the same key is acted on normally.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !sized.(Model).ready {
		t.Fatal("a window size message should make the model ready")
	}
}

package main

import (
	"io"
	"strings"
	"testing"
)

// Because run takes its inputs as parameters, startup failures are testable
// without touching the real environment or exiting the test process.
func TestRunRejectsBadInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		environ []string
		wantErr string
	}{
		{"unknown command", []string{"deploy"}, nil, `unknown command "deploy"`},
		{"missing config", []string{"api"}, nil, "DATABASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := run(t.Context(), tt.args, tt.environ, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("run() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

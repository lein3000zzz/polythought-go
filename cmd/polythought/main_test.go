package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigFlag(t *testing.T) {
	tests := []struct {
		args     []string
		wantArgs []string
		wantPath string
	}{
		{nil, nil, ""},
		{[]string{"serve"}, []string{"serve"}, ""},
		{[]string{"--config", "my.yml", "serve"}, []string{"serve"}, "my.yml"},
		{[]string{"--config=my.yml"}, []string{}, "my.yml"},
		{[]string{"-config", "my.yml", "admin", "create"}, []string{"admin", "create"}, "my.yml"},
		// After the command, flags are the command's.
		{[]string{"demo", "--config", "x"}, []string{"demo", "--config", "x"}, ""},
	}

	for _, tt := range tests {
		args, path := configFlag(tt.args)
		assert.Equal(t, tt.wantArgs, args, "%v", tt.args)
		assert.Equal(t, tt.wantPath, path, "%v", tt.args)
	}
}

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptimizer(t *testing.T) {
	// Sends real ICMP/UDP probes from dummy0 and loops until the probe cache
	// fills; it hangs on runners without working IPv6. Opt-in.
	if os.Getenv("BOXVECTOR_PROBE_TESTS") != "1" {
		t.Skip("set BOXVECTOR_PROBE_TESTS=1 (needs root, dummy0 and IPv6)")
	}
	args := []string{
		"--verbose",
	}
	files, err := filepath.Glob("../tests/probe-*.yml")
	assert.Nil(t, err)
	assert.GreaterOrEqual(t, 1, len(files))

	// cobra flags are package globals: TestGenerate leaves --dry-run set,
	// which would stop the pre-optimizer generate from writing peer files.
	dryRun = false
	defer func() { dryRun = false }()

	for _, testFile := range files {
		// Run pathvector to generate config first, so there is a config to modify
		rootCmd.SetArgs(append(args, []string{
			"generate",
			"--config", testFile,
		}...))
		t.Logf("Running pre-optimizer generate: %v", args)
		assert.Nil(t, rootCmd.Execute())

		args = append(args, []string{
			"optimizer",
			"--config", testFile,
		}...)
		t.Logf("running probe integration with args %v", args)
		rootCmd.SetArgs(args)
		assert.Nil(t, rootCmd.Execute())

		// Check if local pref is lowered
		checkFile, err := os.ReadFile("test-cache/AS65510_EXAMPLE.conf")
		assert.Nil(t, err)
		if !strings.Contains(string(checkFile), "bgp_local_pref = 80; # pathvector:localpref") {
			t.Errorf("expected bgp_local_pref = 80 but not found in file")
		}
	}
}

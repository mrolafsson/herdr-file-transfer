package main

import (
	"os"
	"testing"
)

// TestMain gives the tests a server name of their own: what's drawn, and
// what fits, mustn't depend on the machine they run on.
func TestMain(m *testing.M) {
	hostname = func() (string, error) { return "devbox", nil }
	os.Exit(m.Run())
}

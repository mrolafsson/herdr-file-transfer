package main

// clientAddrs: macOS has no /proc to read another process's env from, so on
// a Mac the sender is config.json's, or the server's own SSH_CONNECTION.
func clientAddrs() []clientAddr { return nil }

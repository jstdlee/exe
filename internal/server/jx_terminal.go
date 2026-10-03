package server

// termDim accepts a VM terminal size from the browser (terminal.go): a
// glitch (0, negative, absurdly large) would garble the remote screen.
func termDim(n int) bool { return n > 0 && n <= 1000 }

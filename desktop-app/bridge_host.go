//go:build stmbridge && !android && !stmandroid && !ios

package main

const bridgeRelisten = false

// bridgePlatformInit has nothing to set up on a desktop host: the bridge
// build is run there only to try the web frontend in a normal browser.
func bridgePlatformInit() {}

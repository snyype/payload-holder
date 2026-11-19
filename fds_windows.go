package main

// keepFDsFromChildren is only needed on Unix (see fds_unix.go); Windows handles aren't inherited
// unless asked for.
func keepFDsFromChildren() {}

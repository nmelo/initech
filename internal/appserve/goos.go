package appserve

import "runtime"

// goos is the system the build runs on, as a var a test can set.
var goos = runtime.GOOS

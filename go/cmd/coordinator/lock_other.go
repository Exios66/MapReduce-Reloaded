//go:build !unix

package main

import "os"

// lock does nothing where there is no flock: an operator must not start
// two coordinators on one -data-dir there.
func lock(string) (*os.File, error) { return nil, nil }

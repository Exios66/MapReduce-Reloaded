package api

import (
	"maps"
	"slices"
)

var registry = map[string]Program{}

// Register makes a program available by name, the name a JobSpec on the wire
// and the mr command use. It is meant to be called from an init function,
// and panics if the name is empty or already registered.
func Register(name string, p Program) {
	if _, dup := registry[name]; dup || name == "" {
		panic("api: Register called twice or with an empty name: " + name)
	}
	registry[name] = p
}

// Lookup returns the program registered under name.
func Lookup(name string) (Program, bool) {
	p, ok := registry[name]
	return p, ok
}

// Programs returns the names of all registered programs, sorted.
func Programs() []string { return slices.Sorted(maps.Keys(registry)) }

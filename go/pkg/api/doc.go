// Package api is the public programming model for MapReduce-Reloaded.
//
// It mirrors §2 of Dean & Ghemawat (OSDI 2004):
//
//	map    (k1, v1)       -> list(k2, v2)
//	reduce (k2, list(v2)) -> list(v2)
//
// User programs implement the Mapper and Reducer interfaces defined in this
// package (added in Phase 1); everything else in the system exists to run
// those two functions reliably across many machines.
package api

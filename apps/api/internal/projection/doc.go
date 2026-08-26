// Package projection derives read-only query models from durable execution
// facts.
//
// In this package, a projection is the deterministic result of reducing a Run
// record, its ordered durable events, and related authoritative records into a
// shape that is convenient to query. It is not a separately mutable domain
// object, a persisted projection table, or a second source of truth. Callers
// rebuild projections from the authoritative records at an event sequence
// watermark; the AsOfSequence fields make that boundary explicit.
package projection

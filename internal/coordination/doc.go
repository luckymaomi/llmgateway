// Package coordination owns rebuildable cross-instance rate buckets and
// concurrency leases. Durable acceptance, quota, and usage facts remain in
// PostgreSQL; a Valkey failure therefore denies admission instead of bypassing
// limits.
package coordination

//go:build !linux && !darwin

package index

// classifyNonLocal is a no-op stub so the package builds under non-target GOOS.
func classifyNonLocal(string) string { return "" }

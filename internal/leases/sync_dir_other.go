//go:build !unix

package leases

func syncDirectory(string) error { return nil }

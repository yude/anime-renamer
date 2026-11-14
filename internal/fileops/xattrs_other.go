//go:build !linux && !darwin

package fileops

func PreserveXattrs(string, string) error { return nil }

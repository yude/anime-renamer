//go:build !linux && !darwin

package fileops

import "os"

func PreserveOwner(string, os.FileInfo) error { return nil }

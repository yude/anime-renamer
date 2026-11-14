//go:build linux || darwin

package fileops

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

const maxAttributeBytes = 16 << 20

func attributeNames(path string) ([]string, error) {
	size, err := unix.Listxattr(path, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if size > 64<<10 {
		return nil, fmt.Errorf("extended attribute names exceed size limit: %s", path)
	}
	data := make([]byte, size)
	n, err := unix.Listxattr(path, data)
	if err != nil {
		return nil, err
	}
	if n < 0 || n > len(data) {
		return nil, fmt.Errorf("extended attribute list changed while reading: %s", path)
	}
	return strings.FieldsFunc(string(data[:n]), func(r rune) bool { return r == 0 }), nil
}

func attributeValue(path, name string) ([]byte, error) {
	size, err := unix.Getxattr(path, name, nil)
	if err != nil {
		return nil, err
	}
	if size > maxAttributeBytes {
		return nil, fmt.Errorf("extended attribute exceeds size limit: %s", name)
	}
	data := make([]byte, size)
	n, err := unix.Getxattr(path, name, data)
	if err != nil {
		return nil, err
	}
	if n < 0 || n > len(data) {
		return nil, fmt.Errorf("extended attribute changed while reading: %s", name)
	}
	return data[:n], nil
}

// PreserveXattrs also preserves POSIX ACLs represented as extended attributes.
// Fail before replacement if an attribute cannot be preserved; never silently
// drop it or inherit a new permission grant from a newly-created temp file.
func PreserveXattrs(source, destination string) error {
	names, err := attributeNames(source)
	if err != nil {
		return err
	}
	values := make(map[string][]byte, len(names))
	total := 0
	for _, name := range names {
		value, err := attributeValue(source, name)
		if err != nil {
			return err
		}
		total += len(value)
		if total > maxAttributeBytes {
			return fmt.Errorf("extended attributes exceed aggregate size limit")
		}
		values[name] = value
	}
	destNames, err := attributeNames(destination)
	if err != nil {
		return err
	}
	for _, name := range destNames {
		if _, ok := values[name]; !ok {
			if err := unix.Removexattr(destination, name); err != nil {
				return err
			}
		}
	}
	for name, value := range values {
		if existing, err := attributeValue(destination, name); err == nil && bytes.Equal(existing, value) {
			continue
		}
		if err := unix.Setxattr(destination, name, value, 0); err != nil {
			return err
		}
	}
	return nil
}

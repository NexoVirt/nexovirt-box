package box

import "golang.org/x/sys/unix"

// renameNoReplace moves without ever replacing an existing target.
func renameNoReplace(fromFD int, from string, toFD int, to string) error {
	return unix.Renameat2(fromFD, from, toFD, to, unix.RENAME_NOREPLACE)
}

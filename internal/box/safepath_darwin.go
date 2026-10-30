package box

import "golang.org/x/sys/unix"

// renameNoReplace moves without ever replacing an existing target (development builds on a Mac).
func renameNoReplace(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_EXCL)
}

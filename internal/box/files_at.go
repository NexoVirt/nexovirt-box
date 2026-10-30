package box

// Descriptor-relative helpers for the file actions. A path is checked with resolve first, then
// every action runs on the descriptor openBeneath returns: no step follows a symlink, so a
// folder swapped for a link after the check (an app container can write into its data folder)
// is refused instead of leading out of the allowed folder.

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

var errStopWalk = errors.New("stop")

// beneath opens the parent of real (inside root r) and returns it with the base name.
func beneath(r rootInfo, real string) (int, string, error) {
	return openBeneath(r.real, real)
}

// openStart is beneath for walks, which may also start at the root itself (name ".").
func openStart(r rootInfo, real string) (int, string, error) {
	if real != r.real {
		return beneath(r, real)
	}
	fd, err := unix.Open(r.real, dirFlags, 0)
	if err != nil {
		return -1, "", uerr("The folder cannot be opened.")
	}
	return fd, ".", nil
}

// ownLikeParentAt gives a new entry the owner of its folder (Box runs as root).
func ownLikeParentAt(dirFD int, name string) {
	var st unix.Stat_t
	if unix.Fstat(dirFD, &st) == nil {
		unix.Fchownat(dirFD, name, int(st.Uid), int(st.Gid), unix.AT_SYMLINK_NOFOLLOW)
	}
}

func lstatAt(dirFD int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	return st, err
}

func isDirMode(m uint32) bool { return m&unix.S_IFMT == unix.S_IFDIR }

// openFileAt opens a regular file below dirFD for reading, never a link, a pipe or a device.
func openFileAt(dirFD int, name string) (*os.File, error) {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, unix.EINVAL
	}
	unix.SetNonblock(fd, false)
	return os.NewFile(uintptr(fd), name), nil
}

// walkAt calls fn for name below dirFD and, for a folder, everything inside it (pre-order).
// Links are reported, never followed. rel is the slash path below the start.
func walkAt(dirFD int, name, rel string, fn func(parentFD int, name, rel string, st unix.Stat_t) error) error {
	st, err := lstatAt(dirFD, name)
	if err != nil {
		return err
	}
	if err := fn(dirFD, name, rel, st); err != nil || !isDirMode(uint32(st.Mode)) {
		return err
	}
	fd, err := unix.Openat(dirFD, name, dirFlags, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	names, err := readDirNamesFD(fd)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := walkAt(fd, n, rel+"/"+n, fn); err != nil {
			return err
		}
	}
	return nil
}

// copyAt copies sName below sFD to dName below dFD. Links are copied as links; sockets, devices
// and pipes are skipped; an existing target is never replaced.
func copyAt(sFD int, sName string, dFD int, dName string) error {
	st, err := lstatAt(sFD, sName)
	if err != nil {
		return err
	}
	perm := uint32(st.Mode) & 0o7777 &^ (unix.S_ISUID | unix.S_ISGID)
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		buf := make([]byte, 4096)
		n, err := unix.Readlinkat(sFD, sName, buf)
		if err != nil {
			return err
		}
		return unix.Symlinkat(string(buf[:n]), dFD, dName)
	case unix.S_IFDIR:
		if err := unix.Mkdirat(dFD, dName, perm|0o700); err != nil {
			return err
		}
		sd, err := unix.Openat(sFD, sName, dirFlags, 0)
		if err != nil {
			return err
		}
		defer unix.Close(sd)
		dd, err := unix.Openat(dFD, dName, dirFlags, 0)
		if err != nil {
			return err
		}
		defer unix.Close(dd)
		names, err := readDirNamesFD(sd)
		if err != nil {
			return err
		}
		for _, n := range names {
			if err := copyAt(sd, n, dd, n); err != nil {
				return err
			}
		}
		return unix.Fchmodat(dFD, dName, perm, 0)
	case unix.S_IFREG:
		in, err := openFileAt(sFD, sName)
		if err != nil {
			return err
		}
		defer in.Close()
		ofd, err := unix.Openat(dFD, dName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, perm)
		if err != nil {
			return err
		}
		out := os.NewFile(uintptr(ofd), dName)
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return nil
}

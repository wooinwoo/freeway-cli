// SPDX-License-Identifier: MIT

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func mkdirPrivate(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	// Protect the DACL from the parent and pass it on to newly created state files.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: sd}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	err = windows.CreateDirectory(name, &attributes)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		// Open will check the existing directory before touching any state files.
		return nil
	}
	return err
}

func lockExclusive(file *os.File) error {
	var offset windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset)
	runtime.KeepAlive(file)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrLocked
	}
	return err
}

func requirePrivate(root *os.Root, name string, _ os.FileInfo) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	trusted := func(sid *windows.SID) bool {
		return sid.Equals(user.User.Sid) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)
	}
	unsafePermissions := errors.New("gateway data must use a private Windows directory ACL for the current user, SYSTEM and Administrators")
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !trusted(owner) {
		return unsafePermissions
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return unsafePermissions
	}
	for i := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return unsafePermissions
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		creator := sid.IsWellKnown(windows.WinCreatorOwnerSid) && ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0
		if !trusted(sid) && !creator {
			return unsafePermissions
		}
	}
	runtime.KeepAlive(sd)
	return nil
}

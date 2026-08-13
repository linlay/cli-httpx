//go:build windows

package app

import (
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var moveFileExWProc = kernel32DLL.NewProc("MoveFileExW")

func publishDownloadedFile(tempPath, targetPath string, overwrite bool) error {
	from, err := syscall.UTF16PtrFromString(tempPath)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(targetPath)
	if err != nil {
		return err
	}
	flags := uintptr(moveFileWriteThrough)
	if overwrite {
		flags |= moveFileReplaceExisting
	}
	result, _, callErr := moveFileExWProc.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), flags)
	if result != 0 {
		return nil
	}
	if callErr != syscall.Errno(0) {
		return callErr
	}
	return syscall.EINVAL
}

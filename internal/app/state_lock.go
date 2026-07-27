package app

import (
	"fmt"
	"os"
)

type stateFileLock struct {
	file *os.File
}

func acquireStateLock(dir, profileName string) (*stateFileLock, error) {
	if err := ensureStateDirectory(dir); err != nil {
		return nil, err
	}
	path := statePath(dir, profileName) + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %v", pathErrorCause(err))
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("set state lock permissions: %v", pathErrorCause(err))
	}
	if err := lockStateFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock state: %v", pathErrorCause(err))
	}
	return &stateFileLock{file: file}, nil
}

func ensureStateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %v", pathErrorCause(err))
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set state directory permissions: %v", pathErrorCause(err))
	}
	return nil
}

func (l *stateFileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockStateFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

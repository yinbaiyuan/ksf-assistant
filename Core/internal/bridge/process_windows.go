//go:build windows

package bridge

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type transientProcessTree struct{ job windows.Handle }

func prepareTransientProcessTree(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func attachTransientProcessTree(command *exec.Cmd) (transientProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return transientProcessTree{}, err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	)
	if err != nil {
		windows.CloseHandle(job)
		return transientProcessTree{}, err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(job)
		return transientProcessTree{}, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return transientProcessTree{}, err
	}
	return transientProcessTree{job: job}, nil
}

func terminateTransientProcessTree(tree transientProcessTree, command *exec.Cmd) {
	if tree.job != 0 {
		_ = windows.TerminateJobObject(tree.job, 1)
		return
	}
	_ = command.Process.Kill()
}

func closeTransientProcessTree(tree transientProcessTree) {
	if tree.job != 0 {
		_ = windows.CloseHandle(tree.job)
	}
}

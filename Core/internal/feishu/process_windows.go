//go:build windows

package feishu

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processTree struct{ job windows.Handle }

func prepareProcessTree(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func attachProcessTree(command *exec.Cmd) (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return processTree{}, err
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
		return processTree{}, err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(job)
		return processTree{}, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return processTree{}, err
	}
	return processTree{job: job}, nil
}

func interruptProcessTree(processTree, *exec.Cmd) {}

func killProcessTree(tree processTree, command *exec.Cmd) {
	if tree.job != 0 {
		_ = windows.TerminateJobObject(tree.job, 1)
		return
	}
	_ = command.Process.Kill()
}

func closeProcessTree(tree processTree) {
	if tree.job != 0 {
		_ = windows.CloseHandle(tree.job)
	}
}

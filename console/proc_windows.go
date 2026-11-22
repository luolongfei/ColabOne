//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	procKernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = procKernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = procKernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = procKernel32.NewProc("AssignProcessToJobObject")
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                jobObjectIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func hideProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// attachProcessLifetime binds the child bridge process to an anonymous Win32
// Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so Windows automatically
// terminates the Python bridge if ColabOne.exe exits unexpectedly.
func attachProcessLifetime(cmd *exec.Cmd) func() {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if procCreateJobObjectW.Find() != nil ||
		procSetInformationJobObject.Find() != nil ||
		procAssignProcessToJobObject.Find() != nil {
		return nil
	}
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return nil
	}
	const (
		jobObjectExtendedLimitInfoClass = 9
		jobObjectLimitKillOnJobClose    = 0x00002000
		processSetQuota                 = 0x0100
		processTerminate                = 0x0001
	)
	var info jobObjectExtendedLimitInformation
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, _ := procSetInformationJobObject.Call(
		job,
		jobObjectExtendedLimitInfoClass,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return nil
	}
	procHandle, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return nil
	}
	assigned, _, _ := procAssignProcessToJobObject.Call(job, uintptr(procHandle))
	_ = syscall.CloseHandle(procHandle)
	if assigned == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return nil
	}
	return func() {
		_ = syscall.CloseHandle(syscall.Handle(job))
	}
}

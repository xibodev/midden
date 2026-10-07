//go:build windows

package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processJob holds the kernel in a job that Windows ends when midden-ui
// ends, however midden-ui ends, so no kernel outlives the App.
type processJob struct{ handle windows.Handle }

func newProcessJob() processJob {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return processJob{}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(handle, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(handle)
		return processJob{}
	}
	return processJob{handle: handle}
}

// add puts process in the job; a process the job cannot take still runs.
func (j processJob) add(process *os.Process) {
	if j.handle == 0 {
		return
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	_ = windows.AssignProcessToJobObject(j.handle, handle)
}

func (j processJob) close() {
	if j.handle != 0 {
		windows.CloseHandle(j.handle)
	}
}

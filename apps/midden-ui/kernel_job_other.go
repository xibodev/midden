//go:build !windows

package main

import "os"

// processJob does nothing here: a kernel left behind by a midden-ui that was
// killed is asked to stop when the App next starts.
type processJob struct{}

func newProcessJob() processJob { return processJob{} }

func (processJob) add(*os.Process) {}

func (processJob) close() {}

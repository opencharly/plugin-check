package check

import (
	"reflect"
	"testing"
)

// TestVmBuildArgsThreadsFromSnapshot gates the phase-3 threading: the vm-build step
// passes --from-snapshot <tag> ONLY when the deploy carries FromSnapshot. Removing
// the threading in bed_run.go fails this test.
func TestVmBuildArgsThreadsFromSnapshot(t *testing.T) {
	plain := vmBuildArgs("cachyos-vm", "")
	wantPlain := []string{"vm", "build", "cachyos-vm"}
	if !reflect.DeepEqual(plain, wantPlain) {
		t.Errorf("plain args = %v, want %v", plain, wantPlain)
	}
	clone := vmBuildArgs("cachyos-vm", "golden")
	wantClone := []string{"vm", "build", "cachyos-vm", "--from-snapshot", "golden"}
	if !reflect.DeepEqual(clone, wantClone) {
		t.Errorf("clone args = %v, want %v", clone, wantClone)
	}
}

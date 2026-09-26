package check

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// venue_kubevirt_test.go — pins the kubevirt bed arm's classifier: a kubevirt deploy (ssh
// TRANSPORT venue carrying the `kubevirt` venue token, and NOT the ExclusiveVenue host-libvirt
// vm) must classify as kubevirt everywhere the runner/live-gather branch. The fixtures are
// built through the REAL trait derivation (desc), so a wrong/missing live trait is caught.

func kubevirtTestTree() map[string]spec.DeployNode {
	return map[string]spec.DeployNode{
		"check-kv":         {Target: "kubevirt", From: "kv-template", Descent: desc("kubevirt")},
		"check-kv-nested":  {Target: "kubevirt", Descent: desc("kubevirt"), Member: []spec.Member{{Name: "inner", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Descent: desc("container")}}}},
		"check-libvirt-vm": {Target: "vm", From: "arch-vm", Descent: desc("ssh")},
		"check-pod":        {Target: "pod", Descent: desc("container")},
	}
}

// TestIsKubeVirtDeployNode pins the substrate classifier: ssh transport without the
// ExclusiveVenue host-lease trait is kubevirt; the host-libvirt vm and a pod are not.
func TestIsKubeVirtDeployNode(t *testing.T) {
	tree := kubevirtTestTree()
	kv := tree["check-kv"]
	if !isKubeVirtDeployNode(&kv) {
		t.Fatal("a kubevirt deploy must classify as kubevirt")
	}
	vm := tree["check-libvirt-vm"]
	if isKubeVirtDeployNode(&vm) {
		t.Fatal("the host-libvirt vm must NOT classify as kubevirt")
	}
	pod := tree["check-pod"]
	if isKubeVirtDeployNode(&pod) {
		t.Fatal("a pod must NOT classify as kubevirt")
	}
}

// TestIsKubeVirtNode pins the dotted-name resolver: a kubevirt ROOT is found by its own name,
// and the host-libvirt vm root is excluded (so it still takes the libvirt arm).
func TestIsKubeVirtNode(t *testing.T) {
	tree := kubevirtTestTree()
	if !isKubeVirtNode(tree, "check-kv") {
		t.Fatal("check-kv (kubevirt root) must resolve as kubevirt")
	}
	if !isKubeVirtNode(tree, "check-kv-nested") {
		t.Fatal("a kubevirt root with a nested pod child must still resolve as kubevirt")
	}
	if isKubeVirtNode(tree, "check-libvirt-vm") {
		t.Fatal("the host-libvirt vm must NOT resolve as kubevirt (it takes the libvirt arm)")
	}
	if isKubeVirtNode(tree, "check-pod") {
		t.Fatal("a pod must NOT resolve as kubevirt")
	}
}

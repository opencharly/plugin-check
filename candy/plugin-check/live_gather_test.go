package check

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// TestPluginResolveVmTarget_LeafVmUnderNonVmParent ports charly/check_cmd_test.go's
// TestResolveVmTarget_LeafVmUnderNonVmParent (K1-unblock wave, arm 1) — covers RCA #13+#14
// (FINAL/K5 unit 6a): a dotted check-live name whose LEAF (not the root) is the vm — a Children
// entry nested under a non-vm parent, e.g. check-sidecar-pod.check-sidecar-pod-ephvm. Before RCA
// #13's fix, resolveVmTarget checked only the root's venue (same defect class as RCA #12's
// checkVmTarget/checkLocalTarget), so vmName never resolved past the raw dotted string. Before
// RCA #14's fix, nestedLeaf stayed nil for this shape, so loadVmCheckPlans fell through to
// deploykit.FindVmDeployNode's AMBIGUOUS top-level From-scan, which could nondeterministically
// pull in an UNRELATED same-base top-level vm's hoisted plan (Go map iteration order, proven
// live). VmSpec resolution is a SEPARATE step now (pluginResolveVmSpec, off the envelope's
// rp.Templates.VM) — this test exercises ONLY the vmName/domainID/nestedLeaf tree-walk these
// fixes target, mirroring the core original's own scoping.
func TestPluginResolveVmTarget_LeafVmUnderNonVmParent(t *testing.T) {
	// A loader-stamped node ALWAYS carries a non-nil .Descent (venue.go's header comment) — the
	// plugin's nodeTraits reads ONLY .Descent.Venue, never a Target-derived fallback (that
	// synthetic-node fallback is core-only, for un-stamped nodes built outside the loader; this
	// package never builds one). So a hand-built test tree must stamp .Descent itself, mirroring
	// what the host's stampFleetDescents pass would produce for the given Target.
	sshDescent := &spec.DescentDescriptor{Venue: "ssh"}
	tree := map[string]spec.FleetNode{
		"web-pod": {Target: "pod", Descent: &spec.DescentDescriptor{Venue: "container"}, Member: []spec.Member{
			{Name: "web-pod-vm", Position: spec.PositionInSubstrate, Node: &spec.FleetNode{Target: "vm", From: "eval-vm", Descent: sshDescent}},
		}},
		"k3s-vm": {Target: "vm", From: "k3s-vm-entity", Descent: sshDescent, Member: []spec.Member{
			{Name: "inner-app", Position: spec.PositionInSubstrate, Node: &spec.FleetNode{Target: "local", Descent: &spec.DescentDescriptor{Venue: "host"}}},
		}},
	}

	t.Run("leaf-vm-under-pod (RCA #13's new exposure)", func(t *testing.T) {
		vmName, domainID, nestedLeaf := pluginResolveVmTarget(tree, "web-pod.web-pod-vm")
		if vmName != "eval-vm" {
			t.Errorf("vmName = %q, want %q (the leaf's own entity, not the raw dotted string)", vmName, "eval-vm")
		}
		if want := vmshared.VmDomainIdentity("web-pod.web-pod-vm"); domainID != want {
			t.Errorf("domainID = %q, want %q (full dotted path, sanitized — the RCA #6-#9 canonical scheme)", domainID, want)
		}
		// RCA #14: nestedLeaf MUST be the tree-walked leaf itself here — never nil — so
		// loadVmCheckPlans sources Plan/AddCandy directly from it instead of falling through to
		// FindVmDeployNode's ambiguous scan.
		if nestedLeaf == nil {
			t.Fatal("nestedLeaf = nil, want the resolved web-pod-vm leaf node (RCA #14: must bypass the ambiguous top-level From-scan)")
		}
		if nestedLeaf.Target != "vm" || nestedLeaf.From != "eval-vm" {
			t.Errorf("nestedLeaf = %+v, want the web-pod-vm leaf (Target=vm, From=eval-vm)", nestedLeaf)
		}
		if len(nestedLeaf.Plan) != 0 {
			t.Errorf("nestedLeaf.Plan = %v, want empty (this member authors no plan of its own)", nestedLeaf.Plan)
		}
	})

	t.Run("root-vm-with-guest-suffix (preserved delegate-into-guest precedent)", func(t *testing.T) {
		vmName, domainID, nestedLeaf := pluginResolveVmTarget(tree, "k3s-vm.inner-app")
		if vmName != "k3s-vm-entity" {
			t.Errorf("vmName = %q, want %q (the ROOT's entity — the leaf is not itself a vm)", vmName, "k3s-vm-entity")
		}
		if want := vmshared.VmDomainIdentity("k3s-vm"); domainID != want {
			t.Errorf("domainID = %q, want %q (the vm ROOT owns the live domain)", domainID, want)
		}
		if nestedLeaf == nil {
			t.Fatal("nestedLeaf = nil, want the resolved inner-app node (guest-nested delegation)")
		}
		if nestedLeaf.Target != "local" {
			t.Errorf("nestedLeaf.Target = %q, want %q", nestedLeaf.Target, "local")
		}
	})
}

// TestPluginGuestNestedCheckCmd ports charly/check_cmd_test.go's TestGuestNestedCheckCmd —
// verifies the guest-side `charly check live` command pluginCheckLiveVM dispatches for a
// nested-in-VM pod (Cutover 6 delegation). The host's format/section/filter/instance selectors
// must pass through, single-quoted, so the guest produces the same report shape the host would.
// guestNestedCheckCmd itself is cmd_helpers.go's Unit A port (unchanged signature).
func TestPluginGuestNestedCheckCmd(t *testing.T) {
	cases := []struct {
		name     string
		pod      string
		format   string
		section  string
		filter   []string
		instance string
		vars     map[string]string
		want     string
	}{
		{
			name:   "minimal (default text)",
			pod:    "selkies-kde",
			format: "text",
			want:   "charly check live 'selkies-kde' --format 'text'",
		},
		{
			name:   "empty format defaults to text",
			pod:    "selkies-kde",
			format: "",
			want:   "charly check live 'selkies-kde' --format 'text'",
		},
		{
			name:     "all selectors pass through",
			pod:      "p",
			format:   "json",
			section:  "deploy",
			filter:   []string{"cdp", "wl"},
			instance: "work",
			want:     "charly check live 'p' --format 'json' --section 'deploy' --filter 'cdp' --filter 'wl' -i 'work'",
		},
		{
			name:     "per-run vars pass through sorted",
			pod:      "p",
			format:   "text",
			vars:     map[string]string{"z": "1", "pr": "9345"},
			instance: "work",
			want:     "charly check live 'p' --format 'text' -i 'work' --var 'pr=9345' --var 'z=1'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := guestNestedCheckCmd(tc.pod, tc.format, tc.section, tc.filter, tc.instance, tc.vars)
			if got != tc.want {
				t.Errorf("guestNestedCheckCmd = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPluginVmHostdevCount ports charly/vm_hostdev_test.go's TestVmHostdevCount — pins the
// nil-safety contract of the VM_HOSTDEV_COUNT intent source: a spec with no libvirt block, no
// devices block, or an empty hostdevs list all read as 0 ("no GPU configured for this VM" →
// legit N/A), and a declared hostdevs list reports its length (the GPU check then HARD-FAILS if
// the guest can't see the device).
// TestPluginCheckRunFeatureLive_VmDispatch proves the feature-live ADE path
// dispatches a VM target to the VM arm (featureLiveArm returns "vm") rather than
// the container-only path. Before the fix, the feature-live path called
// deploykit.ResolveContainer directly and a VM target failed with "container ...
// is not running" — there was no dispatch at all, so this test fails without
// the fix (featureLiveArm does not exist).
func TestPluginCheckRunFeatureLive_VmDispatch(t *testing.T) {
	sshDescent := &spec.DescentDescriptor{Venue: "ssh"}
	tree := map[string]spec.FleetNode{
		"check-omarchy-pr-vm":     {Target: "vm", From: "omarchy-vm", Descent: sshDescent},
		"check-omarchy-suite-pod": {Target: "pod", Descent: &spec.DescentDescriptor{Venue: "container"}},
	}
	if arm := featureLiveArm(tree, "check-omarchy-pr-vm"); arm != "vm" {
		t.Fatalf("feature-live VM dispatch: want the VM arm, got %q", arm)
	}
	if arm := featureLiveArm(tree, "check-omarchy-suite-pod"); arm != "pod" {
		t.Fatalf("feature-live VM dispatch: want the pod arm, got %q", arm)
	}
}

func TestPluginVmHostdevCount(t *testing.T) {
	cases := []struct {
		name string
		spec *vmshared.VmSpec
		want int
	}{
		{"nil spec", nil, 0},
		{"nil libvirt", &vmshared.VmSpec{}, 0},
		{"nil devices", &vmshared.VmSpec{Libvirt: &spec.LibvirtDomain{}}, 0},
		{"zero hostdevs", &vmshared.VmSpec{Libvirt: &spec.LibvirtDomain{Devices: &vmshared.LibvirtDevices{}}}, 0},
		{"two hostdevs", &vmshared.VmSpec{Libvirt: &spec.LibvirtDomain{Devices: &vmshared.LibvirtDevices{
			Hostdevs: []vmshared.LibvirtHostdev{{}, {}},
		}}}, 2},
	}
	for _, tc := range cases {
		if got := pluginVmHostdevCount(tc.spec); got != tc.want {
			t.Errorf("%s: pluginVmHostdevCount = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestPluginResolveVmTarget_DeployHop covers the from: name:tag deploy-chain hop (Phase 3): a
// converted bed's from: names the clone-base BED (a deploy whose own from: names the terminal
// kind:vm template) — the live target must hop the chain ONCE so the spec lookup finds the
// template. A from: that is itself missing from the tree IS the template already (no hop) —
// the same single-hop semantics as sdk/loaderkit DeployTargetEntity. Removing the hop fails
// the bed case (vmName would name the clone-base BED, not the template).
func TestPluginResolveVmTarget_DeployHop(t *testing.T) {
	sshDescent := &spec.DescentDescriptor{Venue: "ssh"}
	convTree := map[string]spec.FleetNode{
		// The check bed (the live target) — from: names the clone-base BED.
		"check-instrument-cachyos-vm": {Target: "check", From: "check-vm-clone-base", Descent: sshDescent},
		// The clone-base BED — itself a deploy whose from: names the terminal template.
		"check-vm-clone-base": {Target: "check", From: "cachyos-vm", Descent: sshDescent},
	}

	t.Run("bed-from-bed-hops-to-the-terminal-template", func(t *testing.T) {
		vmName, _, _ := pluginResolveVmTarget(convTree, "check-instrument-cachyos-vm")
		if vmName != "cachyos-vm" {
			t.Fatalf("vmName = %q, want %q (the terminal template after ONE hop)", vmName, "cachyos-vm")
		}
	})

	t.Run("bed-from-template-passes-through", func(t *testing.T) {
		// The old spelling: a bed whose from: names the template directly — the tree lookup
		// misses (a template is not in the Fleet tree), so no hop; the template IS the target.
		plain := map[string]spec.FleetNode{
			"check-plain-bed": {Target: "check", From: "cachyos-vm", Descent: sshDescent},
		}
		vmName, _, _ := pluginResolveVmTarget(plain, "check-plain-bed")
		if vmName != "cachyos-vm" {
			t.Fatalf("vmName = %q, want %q (the from: template, no hop)", vmName, "cachyos-vm")
		}
	})
}

// ---------------------------------------------------------------------------
// The deliberately-stopped ROOT (the preempt semantics, post-unroll) — the
// live-gather root-state handling. The migrate unroll promotes the first member
// to the BED ROOT; for the preempt beds that root IS the preemptible holder the
// bed's own claimant stops DURING bring-up, so the pod live-gather's former
// hard running-root gate (deploykit.ResolveContainer) failed every converted
// preempt bed BEFORE any step could run. The contract below pins the three
// decisions of the fix: WHO qualifies (a DECLARED holder, never a crashed
// undeclared root), HOW the stopped root resolves (existence, not running), and
// WHAT the declared-state gather merges (the authored plans — never the baked
// running-acceptance set).

// TestStoppedHolderRoot pins the discriminator for both fixture shapes the
// ruling names: a PREEMPT-shaped bed (the root is the declared holder — via the
// converted tree node OR the per-host overlay entry persistBedDeployOverrides
// seeds) gathers its declared state, while a NORMAL bed (root running, no
// holder declaration) is unchanged — and every non-holder stop (a crashed
// undeclared root, a claimant root, a missing declaration) stays a hard
// failure, never a silent skip.
func TestStoppedHolderRoot(t *testing.T) {
	holderDecl := &spec.PreemptibleConfig{Holds: []string{"test-lock"}}
	convertedRoot := &spec.FleetNode{Target: "pod", Preemptible: holderDecl} // the unrolled preempt bed ROOT
	overlayOnly := &spec.FleetNode{Target: "pod"}                            // declaration lives only in the seeded overlay
	seededOverlay := &spec.FleetNode{Preemptible: holderDecl}
	normalOverlay := &spec.FleetNode{Target: "pod"}                                          // a normal bed's overlay: no arbitration role
	claimantRoot := &spec.FleetNode{Target: "pod", RequiresExclusive: []string{"test-lock"}} // a claimant root RUNS

	t.Run("preempt-shaped: converted tree root declares the holder", func(t *testing.T) {
		if !stoppedHolderRoot(convertedRoot, normalOverlay) {
			t.Fatal("stoppedHolderRoot = false, want true — the converted preempt bed ROOT authors preemptible.holds; its deliberate stop is the assertion target, not a broken bed")
		}
	})
	t.Run("preempt-shaped: seeded overlay declares the holder", func(t *testing.T) {
		if !stoppedHolderRoot(overlayOnly, seededOverlay) {
			t.Fatal("stoppedHolderRoot = false, want true — persistBedDeployOverrides seeds the member holder role into the per-host overlay; that declaration qualifies alone")
		}
	})
	t.Run("normal bed: root running, no declaration — unchanged behavior", func(t *testing.T) {
		if stoppedHolderRoot(&spec.FleetNode{Target: "pod"}, normalOverlay) {
			t.Fatal("stoppedHolderRoot = true, want false — a bed with no holder declaration keeps the hard running-root gate (a stopped root is a broken bed, never skipped)")
		}
	})
	t.Run("nil surfaces — crashed undeclared root stays a hard failure", func(t *testing.T) {
		if stoppedHolderRoot(nil, nil) {
			t.Fatal("stoppedHolderRoot = true, want false — absent declarations never qualify")
		}
	})
	t.Run("claimant root is not a holder — it stops OTHERS and keeps running", func(t *testing.T) {
		if stoppedHolderRoot(claimantRoot, nil) {
			t.Fatal("stoppedHolderRoot = true, want false — requires_exclusive is the CLAIMANT axis; only Preemptible.Holds declares a stoppable holder")
		}
	})
	t.Run("an empty Holds list is not a declaration", func(t *testing.T) {
		if stoppedHolderRoot(&spec.FleetNode{Preemptible: &spec.PreemptibleConfig{}}, nil) {
			t.Fatal("stoppedHolderRoot = true, want false — Preemptible with no holds declares nothing")
		}
	})
}

// TestResolveContainerDeclared pins the stopped-root resolve: the SAME
// engine + container-name synthesis as deploykit.ResolveContainer, gated on
// EXISTENCE (RUNNING OR STOPPED) instead of the running gate — a deliberately
// stopped holder's container still exists and its image identity stays readable
// via inspect, while a container that does not exist at all stays a hard error.
// The running probe must NEVER be consulted on this path (that is the whole
// fix), so the test fails the test if it fires.
func TestResolveContainerDeclared(t *testing.T) {
	origRuntime, origExists, origRunning := kit.ResolveRuntime, kit.ContainerExists, kit.ContainerRunning
	t.Cleanup(func() {
		kit.ResolveRuntime, kit.ContainerExists, kit.ContainerRunning = origRuntime, origExists, origRunning
	})

	kit.ResolveRuntime = func() (*kit.ResolvedRuntime, error) { return &kit.ResolvedRuntime{RunEngine: "podman"}, nil }
	existsCalls := map[string]int{}
	kit.ContainerExists = func(engine, name string) bool { existsCalls[name]++; return name == "charly-holder-bed" }
	runningCalls := 0
	kit.ContainerRunning = func(engine, name string) bool { runningCalls++; return false }

	t.Run("stopped-but-existing holder resolves WITHOUT the running gate", func(t *testing.T) {
		engine, name, err := resolveContainerDeclared("holder-bed", "")
		if err != nil {
			t.Fatalf("resolveContainerDeclared(holder-bed) = %v, want nil (the container EXISTS — stopped is the declared state)", err)
		}
		if engine != "podman" || name != "charly-holder-bed" {
			t.Fatalf("resolveContainerDeclared = %s/%s, want podman/charly-holder-bed (the SAME synthesis as deploykit.ResolveContainer)", engine, name)
		}
		if runningCalls != 0 {
			t.Fatalf("kit.ContainerRunning fired %d time(s) on the declared-resolve path — the fix must gate on EXISTENCE only", runningCalls)
		}
		if existsCalls["charly-holder-bed"] == 0 {
			t.Fatal("kit.ContainerExists never consulted — the existence gate is missing")
		}
	})
	t.Run("a container that does not exist stays a hard error", func(t *testing.T) {
		_, _, err := resolveContainerDeclared("ghost-bed", "")
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("resolveContainerDeclared(ghost-bed) = %v, want a does-not-exist error (a missing container is never a declared state)", err)
		}
	})
}

// TestPodLiveGatherBakedSet pins WHAT the declared-state gather merges: a
// RUNNING root gathers the image's baked running-acceptance set (unchanged);
// a DELIBERATELY-STOPPED holder root merges nil in its place — the baked set is
// the box's RUNNING acceptance (live_image.go's contract), which a stopped
// root cannot answer, so the gather owes only the authored plans. In the
// preempt shape the root authors none: the honest NoSteps outcome.
func TestPodLiveGatherBakedSet(t *testing.T) {
	baked := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "baked:holder-bed"}}}
	meta := &spec.BoxMetadata{Description: baked}
	if got := podLiveGatherBakedSet(meta, false); got != baked {
		t.Fatal("podLiveGatherBakedSet(running) != the baked set — the RUNNING path must be byte-identical to the old merge")
	}
	if got := podLiveGatherBakedSet(meta, true); got != nil {
		t.Fatalf("podLiveGatherBakedSet(stopped) = %+v, want nil — a deliberately-stopped holder root is not in its running acceptance state; the baked plan must not run against it", got)
	}
	if got := podLiveGatherBakedSet(nil, true); got != nil {
		t.Fatal("podLiveGatherBakedSet(nil, stopped) = non-nil, want nil (nil-safe)")
	}
}

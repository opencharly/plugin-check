package check

import (
	"strings"
	"testing"
)

// The bed diagnostics gate exists because a bed once reported ok:true over 56 error lines: the
// runner graded exit codes and nothing else. These tests pin the three properties that make the
// gate worth having — errors are never exempt, an allowlisted line is counted separately rather
// than vanishing, and every allowance is narrow enough that it cannot swallow a neighbouring
// finding.

// TestErrorTierEntriesAreConditional replaces TestErrorAllowlistIsEmpty, deliberately and
// visibly — which is the bar the deleted test set for its own removal.
//
// The empty error tier rested on a premise: "an `error:` line under a zero exit code is a
// swallowed failure by construction." pacman falsifies it. Its mirror fallback prints one
// `error: failed retrieving file …` per unreachable mirror and then installs from the next.
//
// So the invariant is no longer emptiness; it is that an error-tier exemption must be CONDITIONAL
// on a proof of recovery. That keeps what emptiness was protecting — the gate cannot be talked
// into tolerating an error — while letting it stop firing on a retry that worked. An
// unconditional error entry would be the weakening, and this test is what blocks one.
func TestErrorTierEntriesAreConditional(t *testing.T) {
	for _, a := range diagnosticAllowlist {
		if a.Severity != severityError {
			continue
		}
		if a.RecoveredBy == "" {
			t.Errorf("allowlist entry %q is error-tier and UNCONDITIONAL; an error exemption must "+
				"carry RecoveredBy so it still fails when the operation did not recover", a.ID)
		}
		if a.Match.NumSubexp() < 1 {
			t.Errorf("allowlist entry %q is conditional but its Match has no capture group; the "+
				"recovery proof must be tied to the subject that failed, not to the log at large",
				a.ID)
		}
	}
}

// TestConditionalErrorFailsWithoutRecovery is the half that makes the conditional form worth
// having: the SAME error line is exempt when the log proves recovery and fatal when it does not.
//
// The fixtures use a HYPHENATED package name deliberately. An earlier version of this test used
// `libyuv` against a wrong-subject line of "installing something-else..." — two names sharing no
// prefix — and so it passed while the implementation was broken for every multi-token name, which
// is most of them. A reviewer caught it by driving the real gate with `nvidia-container-toolkit`:
// the capture stopped at the first hyphen, so "installing nvidia..." discharged the error and
// "installing nvidia-container-toolkit..." did not. A negative case that cannot fail is not a
// negative case.
//
// The verb axis is enumerated for the same reason. The first fix named `installing` and
// `upgrading`, which left the entry too TIGHT in the same edit that stopped it being too loose:
// a `reinstalling` recovery — 23 occurrences in this tree's retained logs — would have failed a
// step for a package that arrived. The table below drives all four installing verbs plus
// `removing`, which must NOT discharge.
func TestConditionalErrorFailsWithoutRecovery(t *testing.T) {
	const pkg = "nvidia-container-toolkit"
	errLine := "error: failed retrieving file '" + pkg + "-1.17.8-1-x86_64.pkg.tar.zst' " +
		"from cdn77.cachyos.org : The requested URL returned error: 404\n"
	const step = "STEP 1/1: RUN pacman -S nvidia-container-toolkit\n"

	// Every alpm package operation that ENDS WITH THE PACKAGE INSTALLED is a recovery. The
	// enumeration is ALPM_EVENT_PACKAGE_OPERATION_START's five, minus REMOVE — naming only
	// `installing` would fail a step for a package that demonstrably arrived, and `reinstalling`
	// alone occurs 23 times in this tree's retained bed logs.
	for _, verb := range []string{"installing", "upgrading", "reinstalling", "downgrading"} {
		t.Run("recovered by "+verb, func(t *testing.T) {
			d := scanStepDiagnostics(step + errLine + verb + " " + pkg + "...\n")
			if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
				t.Errorf("%q is a recovery and must be exempt; got %+v", verb, d)
			}
		})
	}

	// REMOVE is the one operation that leaves the package ABSENT, so it must NOT discharge the
	// error. This is the row that fails if someone ever widens the verb set by reaching for
	// "every line pacman prints about the package" instead of the enumeration above.
	t.Run("removing is not a recovery", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine + "removing " + pkg + "...\n")
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a removal leaves the package absent and must not discharge; got %+v", d)
		}
	})

	t.Run("no recovery at all is fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("an unrecovered retrieval error must still fail the step; got %+v", d)
		}
	})

	// The cases that matter, and that the previous fixture could not reach: a name that shares a
	// PREFIX with the failed package must not discharge it, in either direction.
	for _, wrong := range []string{"nvidia", "nvidia-container", "nvidia-container-toolkit-extra"} {
		t.Run("wrong subject: "+wrong, func(t *testing.T) {
			d := scanStepDiagnostics(step + errLine + "installing " + wrong + "...\n")
			if d.Errors != 1 || d.Allowlisted != 0 {
				t.Errorf("%q must NOT discharge an error for %q; got %+v", wrong, pkg, d)
			}
		})
	}

	// And the single-token name the original fixture used still works, so the fix did not trade
	// one shape for another.
	t.Run("single-token name still recovers", func(t *testing.T) {
		d := scanStepDiagnostics("STEP 1/1: RUN pacman -S libyuv\n" +
			"error: failed retrieving file 'libyuv-r2921+644251f25-1.1-x86_64_v3.pkg.tar.zst' " +
			"from us.cachyos.org : The requested URL returned error: 404\n" +
			"installing libyuv...\n")
		if d.Errors != 0 || d.Allowlisted != 1 {
			t.Errorf("the originally-observed shape must still be exempt; got %+v", d)
		}
	})
}

// TestDnfCommandlineGpgcheckAllowanceIsConditional covers the entry added for dnf's
// "skipped OpenPGP checks for N package from repository: @commandline" warning. dnf prints it
// when a package passed on the command line (a local RPM) has no signature to verify — the
// charly package built from source in the check bed. The entry is CONDITIONAL on the same log
// proving the transaction completed (`Complete!`), so a transaction that never completes still
// fails its step.
func TestDnfCommandlineGpgcheckAllowanceIsConditional(t *testing.T) {
	const warning = "Warning: skipped OpenPGP checks for 1 package from repository: @commandline\n"
	const step = "STEP 1/1: RUN dnf install -y /tmp/charly.rpm\n"

	t.Run("recovered by Complete!", func(t *testing.T) {
		d := scanStepDiagnostics(step + warning + "Complete!\n")
		if d.Warnings != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a completed dnf transaction must exempt the warning; got %+v", d)
		}
	})

	t.Run("no recovery is not exempted", func(t *testing.T) {
		d := scanStepDiagnostics(step + warning)
		if d.Warnings != 1 || d.Allowlisted != 0 {
			t.Errorf("a transaction that never completes must not be exempted; got %+v", d)
		}
		// The warning tier is not fatal under the default policy, but the entry must not claim
		// the line either — and under the promoted policy the unrecovered warning goes red.
		promoted := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}
		if !d.fails(promoted) {
			t.Errorf("an unrecovered warning must fail the step under the promoted policy; got %+v", d)
		}
	})

	// The allowance is anchored to the EXACT dnf message. A different warning line followed by a
	// dnf success must not be exempted — the recovery proves the transaction completed, not that
	// some other warning was harmless.
	t.Run("unrelated warning is not exempted by a dnf success", func(t *testing.T) {
		d := scanStepDiagnostics(step + "Warning: some other dnf warning\n" + "Complete!\n")
		if d.Warnings != 1 || d.Allowlisted != 0 {
			t.Errorf("an unrelated warning must not be discharged by a dnf success; got %+v", d)
		}
	})

	// The count is variable (1 package, 2 packages, …) and the message is a fixed sentence
	// otherwise — the pattern must claim every count.
	t.Run("plural count is claimed", func(t *testing.T) {
		d := scanStepDiagnostics(step + "Warning: skipped OpenPGP checks for 2 packages from repository: @commandline\n" + "Complete!\n")
		if d.Warnings != 0 || d.Allowlisted != 1 {
			t.Errorf("the plural form must be claimed; got %+v", d)
		}
	})
}

// TestPipResolverConflictAllowanceIsConditional covers the entry added for pip's
// dependency-resolver conflict notice. pip prints the notice when the environment ALREADY
// carries packages that conflict with the install's requirements, then installs anyway and
// exits 0 — observed live in the jupyter-ml image-build (vllm's pinned requirements vs the
// pixi.toml loose pins). The entry is CONDITIONAL on the same log proving the install
// completed (`Successfully installed ...`), so an install that never reports success still
// fails its step.
func TestPipResolverConflictAllowanceIsConditional(t *testing.T) {
	const notice = "ERROR: pip's dependency resolver does not currently take into account " +
		"all the packages that are installed. This behaviour is the source of the " +
		"following dependency conflicts.\n"
	const step = "STEP 1/1: RUN pip install fastmcp\n"

	t.Run("recovered by Successfully installed", func(t *testing.T) {
		d := scanStepDiagnostics(step + notice + "Successfully installed fastmcp-3.4.7 mcp-1.29.0\n")
		if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a completed pip install must exempt the notice; got %+v", d)
		}
	})

	t.Run("no recovery is fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + notice)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("an install that never reports success must still fail the step; got %+v", d)
		}
	})

	// The allowance is anchored to the EXACT pip message. A different error line followed by a
	// pip success must not be exempted — the recovery proves the install completed, not that
	// some other error was harmless.
	t.Run("unrelated error is not exempted by a pip success", func(t *testing.T) {
		d := scanStepDiagnostics(step + "ERROR: Could not find a version that satisfies the requirement foo\n" +
			"Successfully installed fastmcp-3.4.7\n")
		if d.Errors != 1 || d.Allowlisted != 0 {
			t.Errorf("an unrelated error must not be discharged by a pip success; got %+v", d)
		}
	})
}

// TestMkinitcpioChrootAutodetectIsConditional proves the mkinitcpio autodetect
// chroot error is exempted ONLY when the initramfs image is actually created.
func TestMkinitcpioChrootAutodetectIsConditional(t *testing.T) {
	const errLine = "==> ERROR: failed to detect root filesystem\n"
	const step = "STEP 1/1: RUN arch-chroot /mnt mkinitcpio -P\n"

	t.Run("recovered by Initcpio image generation successful", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine +
			"==> Creating zstd-compressed initcpio image: '/boot/initramfs-linux.img'\n" +
			"==> Initcpio image generation successful\n")
		if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a completed mkinitcpio build must exempt the autodetect error; got %+v", d)
		}
	})

	t.Run("no recovery is fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a build that never creates the image must still fail the step; got %+v", d)
		}
	})

	t.Run("unrelated error is not exempted by a success", func(t *testing.T) {
		d := scanStepDiagnostics(step + "==> ERROR: missing kernel module\n" +
			"==> Initcpio image generation successful\n")
		if d.Errors != 1 || d.Allowlisted != 0 {
			t.Errorf("an unrelated mkinitcpio error must not be discharged by a success; got %+v", d)
		}
	})
}

// The limine alpm hook cannot succeed inside a pacstrap chroot (no mounted ESP, and
// /etc/default/limine is written later by charly's bootloader phase). Exempting it is only
// legitimate when that phase then DOES write the config against the real ESP — otherwise
// the difference between "chroot artifact" and "the bootloader install is broken" is
// exactly what the bed exists to catch.
func TestLimineEspNotMountedInChrootIsConditional(t *testing.T) {
	// Note the trailing space: limine emits one, and the pattern must tolerate it or the
	// allowance silently never matches and every omarchy pacstrap build fails.
	const errLine = "ERROR: FAT32 boot partition not found. Make sure it is mounted or configure ESP_PATH in /etc/default/limine. \n"
	const step = "STEP 1/1: RUN pacstrap -K /target\n"

	t.Run("recovered by charly writing limine.conf to the real ESP", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine +
			"Boot000F* Omarchy\tHD(1,GPT,c950b917,0x800,0xff800)/\\EFI\\BOOT\\BOOTX64.EFI\n" +
			"Copied: /tmp/limine-mkinitcpio.X/linux.efi -> /boot/EFI/Linux/omarchy_linux.efi\n" +
			"Updated: /boot/limine.conf\n")
		if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a build whose bootloader phase wrote limine.conf must exempt the "+
				"chroot hook error; got %+v", d)
		}
	})

	t.Run("no recovery is fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a build that never writes /boot/limine.conf has a genuinely broken "+
				"bootloader install and must still fail; got %+v", d)
		}
	})

	t.Run("an unrelated limine error is not discharged", func(t *testing.T) {
		d := scanStepDiagnostics(step + "ERROR: limine-install: target is not a block device\n" +
			"Updated: /boot/limine.conf\n")
		if d.Allowlisted != 0 {
			t.Errorf("only the ESP-not-mounted line is exempt; got %+v", d)
		}
	})
}

func TestGrubProbeFuseOverlayfsIsConditional(t *testing.T) {
	const errLine = "/usr/sbin/grub-probe: error: failed to get canonical path of 'fuse-overlayfs'.\n"
	const step = "STEP 1/1: RUN apt-get install -y grub-common\n"

	t.Run("recovered by the image tag", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine +
			"Successfully tagged ghcr.io/opencharly/debian-coder:check-debian-coder-pod\n")
		if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a completed image build must exempt the grub-probe error; got %+v", d)
		}
	})

	t.Run("no recovery is fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a build that never tags the image must still fail the step; got %+v", d)
		}
	})

	t.Run("unrelated error is not exempted by a tag", func(t *testing.T) {
		d := scanStepDiagnostics(step + "/usr/sbin/grub-probe: error: cannot find a GRUB drive\n" +
			"Successfully tagged ghcr.io/opencharly/debian-coder:check-debian-coder-pod\n")
		if d.Errors != 1 || d.Allowlisted != 0 {
			t.Errorf("an unrelated grub-probe error must not be discharged by a tag; got %+v", d)
		}
	})
}

// TestAllowlistEntriesAreWellFormed keeps the audit trail honest: the Why is printed verbatim
// into summary.yml on every run, so an empty or throwaway one silently converts a reviewed
// exemption into an unexplained one.
// TestCachyosLocalNewerAllowances gates the binutils/libtool entries (RCA 2026-09-06): the
// check-instrument-cachyos-vm R10 bed run surfaced them as UN-allowlisted "local newer than
// repo" warnings - the SAME upstream skew the zstd/pacman-contrib entries document. Each
// line MUST be claimed by its own entry (and would fail with the entries absent).
func TestCachyosLocalNewerAllowances(t *testing.T) {
	claimed := map[string]string{
		"warning: binutils: local (2.44-1) is newer than cachyos-v3 (2.43-2)":  "cachyos-binutils-local-newer-than-repo",
		"warning: libtool: local (2.5.4-1) is newer than cachyos-v3 (2.5.3-2)": "cachyos-libtool-local-newer-than-repo",
		"warning: zstd: local (1.5.7-3) is newer than cachyos-v3 (1.5.7-2)":    "cachyos-zstd-local-newer-than-repo",
	}
	for line, wantID := range claimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", line)
		}
		a := allowanceFor(sev, line)
		if a == nil || a.ID != wantID {
			t.Errorf("%q: want the %s allowance, got %v", line, wantID, a)
		}
	}
	unrelated := "warning: gcc: local (14.2.1-2) is newer than cachyos-v3 (14.2.1-1)"
	if sev, _, ok := classifyDiagnosticLine(unrelated); ok {
		if a := allowanceFor(sev, unrelated); a != nil {
			t.Errorf("%q must NOT be claimed by any cachyos-local-newer entry", unrelated)
		}
	}
}

func TestAllowlistEntriesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range diagnosticAllowlist {
		if a.ID == "" || a.Match == nil {
			t.Errorf("allowlist entry %+v: ID and Match are both required", a)
			continue
		}
		if seen[a.ID] {
			t.Errorf("duplicate allowlist ID %q — IDs key the per-run usage report", a.ID)
		}
		seen[a.ID] = true
		if len(a.Why) < 80 {
			t.Errorf("allowlist entry %q: Why is %d chars; it is printed into summary.yml as the "+
				"justification a reader audits, so it must actually explain the exemption",
				a.ID, len(a.Why))
		}
	}
}

// TestPacmanNeededAllowanceIsScoped covers the entry added for pacman's `--needed` acknowledgement
// and, more importantly, its BOUNDARY. `--needed` is what makes a repeated install idempotent, so
// the message is unavoidable on any base that already ships a package a candy declares — but the
// pattern must claim ONLY that sentence. The negative cases are real pacman warnings that share
// its opening words.
func TestPacmanNeededAllowanceIsScoped(t *testing.T) {
	claimed := []string{
		"warning: dbus-1.16.2-1.1 is up to date -- skipping",
		"warning: podman-6.1.0-1.1 is up to date -- skipping",
		"warning: shadow-4.20.0.arch1-1.1 is up to date -- skipping",
		// An EPOCH renders as <name>-<epoch>:<ver>-<rel>. The colon is not decorative:
		// before it was in the class, `rust-1:1.98.0-1.1` fell through and every box
		// installing an epoch-versioned package reported a warning nothing could fix.
		"warning: rust-1:1.98.0-1.1 is up to date -- skipping",
		"warning: ffmpeg-2:9.0.1-1 is up to date -- skipping",
	}
	for _, line := range claimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", line)
		}
		a := allowanceFor(sev, line)
		if a == nil || a.ID != "pacman-needed-package-already-current" {
			t.Errorf("%q: want the pacman-needed allowance, got %v", line, a)
		}
	}

	notClaimed := []string{
		"warning: zstd: local (1.5.7-3) is newer than cachyos-v3 (1.5.7-2)",
		"warning: could not fully load metadata for package foo-1.0-1",
		"warning: database file for 'extra' does not exist (use '-Sy' to download)",
		"warning: dbus-1.16.2-1.1 is up to date -- skipping this and everything after it",
	}
	for _, line := range notClaimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			continue // not recognised as a diagnostic; nothing to exempt
		}
		if a := allowanceFor(sev, line); a != nil && a.ID == "pacman-needed-package-already-current" {
			t.Errorf("%q must NOT be claimed by the pacman-needed allowance", line)
		}
	}
}

// TestPacmanHookFailedMkinitcpioIsConditional proves the pacman hook-wrapper
// error (`error: command failed to execute correctly`) is exempted ONLY when the
// initramfs image is actually created — the same recovery as the mkinitcpio
// autodetect allowance.
func TestPacmanHookFailedMkinitcpioIsConditional(t *testing.T) {
	const errLine = "error: command failed to execute correctly\n"
	const step = "STEP 1/1: RUN pacman -Syu --needed linux\n"

	t.Run("recovered by Initcpio image generation successful", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine +
			"==> Creating zstd-compressed initcpio image: '/boot/initramfs-linux.img'\n" +
			"==> Initcpio image generation successful\n")
		if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a completed hook must exempt the wrapper error; got %+v", d)
		}
	})

	t.Run("no recovery is fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a hook that never creates the image must still fail; got %+v", d)
		}
	})
}

// TestPacmanPostTransactionHookContainerSystemdIsConditional covers the OTHER hook family that
// pacman reports with the same generic wrapper error: the post-transaction kernel-modules hook
// of a CONTAINER build, where there is no systemd as PID 1 to answer. The fixture is the
// verbatim captured sequence from the check-githubrunner-pod image-build (RCA 2026-09-11/12),
// where the log carried this ONE error line and the image itself built and was tagged.
//
// The negative rows are why this is an allowance rather than a blanket pacman exemption: the
// wrapper error with NO refusal in the log still fails the step, and pacman's own real failure
// wording is never claimed — not even when the refusal shares the log.
func TestPacmanPostTransactionHookContainerSystemdIsConditional(t *testing.T) {
	// Captured verbatim from the run whose image tag is `check-githubrunner-pod-2026.254.2125`.
	// The line numbers previously cited here (1842-1846) belong to THAT capture run; in the
	// body's RETAINED run (`2026.255.0829`) the same refusal appears at `first_line: 1703`
	// (`error: command failed to execute correctly`). Citations name their own run.
	const captured = "( 6/10) Loading new kernel modules...\n" +
		"System has not been booted with systemd as init system (PID 1). Can't operate.\n" +
		"Failed to connect to system scope bus via local transport: Host is down\n" +
		"error: command failed to execute correctly\n" +
		"( 7/10) Updating fontconfig configuration...\n"
	const errLine = "error: command failed to execute correctly\n"
	const step = "STEP 12/14: RUN pacman -S --needed ...\n"

	t.Run("the captured container hook sequence is exempt", func(t *testing.T) {
		d := scanStepDiagnostics(step + captured)
		if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("the container hook wrapper error must be exempt; got %+v", d)
		}
		if id := allowIDForLine(d, errLine); id != "pacman-post-transaction-hook-container-systemd" {
			t.Errorf("the wrapper error must be claimed by its OWN entry, got %q", id)
		}
	})

	t.Run("the wrapper error with no refusal in the log is fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("an unexplained hook failure must still fail the step; got %+v", d)
		}
	})

	t.Run("a DIFFERENT hook's refusal does not exempt the wrapper error", func(t *testing.T) {
		// The device-manager hook is refused by systemd in the same container build, but the
		// modules-load hook's OWN banner is absent: the refusal ALONE must not claim a wrapper
		// error that can belong to the device-manager hook instead. This is the boundary the
		// review required — several hooks share the one wrapper wording, so the anchor binds to
		// the hook that names this class.
		const otherHook = "( 6/10) Reloading device manager configuration...\n" +
			"System has not been booted with systemd as init system (PID 1). Can't operate.\n" +
			"error: command failed to execute correctly\n"
		d := scanStepDiagnostics(step + otherHook)
		if d.Errors != 1 || d.Allowlisted != 0 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a refusal from a DIFFERENT hook must leave the wrapper error fatal; got %+v", d)
		}
	})

	t.Run("the refusal does not exempt a REAL pacman failure", func(t *testing.T) {
		const realFailure = "error: failed to commit transaction (conflicting files)\n"
		d := scanStepDiagnostics(step + captured + realFailure)
		if d.Errors != 1 || d.Allowlisted != 1 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a failed transaction must stay fatal; got %+v", d)
		}
		if id := allowIDForLine(d, realFailure); id != "" {
			t.Errorf("pacman's real failure wording must not be claimed, got %q", id)
		}
	})
}

// TestHookWrapperEntriesResolvePerProof pins the resolution rule the two wrapper entries depend
// on. pacman's `error: command failed to execute correctly` is matched by BOTH
// pacman-hook-failed-mkinitcpio-recovered and pacman-post-transaction-hook-container-systemd,
// and which one CLAIMS it is decided by the proof present in the log — never by table order
// alone. With a first-match-wins lookup, adding the second entry would have been a no-op: the
// mkinitcpio entry matches first, its recovery is absent in a container build, and the
// container class would have stayed fatal with the new entry never consulted.
func TestHookWrapperEntriesResolvePerProof(t *testing.T) {
	const errLine = "error: command failed to execute correctly\n"
	const step = "STEP 1/1: RUN pacman -Syu --needed linux\n"

	t.Run("only the mkinitcpio proof present", func(t *testing.T) {
		d := scanStepDiagnostics(step + errLine + "==> Initcpio image generation successful\n")
		if d.Errors != 0 || d.Allowlisted != 1 {
			t.Fatalf("the completed hook must exempt the wrapper error; got %+v", d)
		}
		if id := allowIDForLine(d, errLine); id != "pacman-hook-failed-mkinitcpio-recovered" {
			t.Errorf("want the mkinitcpio entry, got %q", id)
		}
	})

	t.Run("only the container-init proof present", func(t *testing.T) {
		// The container-init proof is the WHOLE chain the entry's RecoveredBy binds to: the
		// modules-load hook's own banner, systemd's refusal, then the wrapper error (verbatim
		// from the captured check-githubrunner-pod image-build).
		const containerChain = "( 6/10) Loading new kernel modules...\n" +
			"System has not been booted with systemd as init system (PID 1). Can't operate.\n" +
			"Failed to connect to system scope bus via local transport: Host is down\n" +
			"error: command failed to execute correctly\n"
		d := scanStepDiagnostics(step + containerChain)
		if d.Errors != 0 || d.Allowlisted != 1 {
			t.Fatalf("the container-init signature must claim the wrapper error; got %+v", d)
		}
		if id := allowIDForLine(d, errLine); id != "pacman-post-transaction-hook-container-systemd" {
			t.Errorf("want the container-systemd entry, got %q", id)
		}
	})
}

// TestPodmanNestedRootfsNotSharedMountAdvisoryIsConditional covers the podman logrus advisory an
// IN-CONTAINER (nested) rootless podman prints when the root filesystem it will bind against is
// not a SHARED mount. The host is not the source — findmnt reports `/` shared there and a
// host-side podman run prints no such line (both checked 2026-09-12) — while inside a build
// container the OCI runtime mounts the rootfs MS_PRIVATE, so the nested podman the
// container-nesting candy runs always prints it and then populates the nested store.
//
// Captured verbatim from the run whose image tag is `check-githubrunner-pod-2026.254.2125`
// (its `level=warning` timestamp is that run's), inside the STEP 56/99 RUN that prefetches
// quay.io/libpod/alpine. In the body's RETAINED run (`2026.255.0829`) the same advisory appears
// at `first_line: 1812`. The line number below names the CAPTURE run, not the retained one.
func TestPodmanNestedRootfsNotSharedMountAdvisoryIsConditional(t *testing.T) {
	// Verbatim from the CAPTURE run named above. The escaped quote pair around the slash is
	// podman's logrus rendering of the mount path the advisory names.
	const advisory = `time="2026-09-11T21:31:45Z" level=warning msg="\"/\" is not a shared mount, this could cause issues or missing mounts with rootless containers"` + "\n"
	const step = "[23/23] STEP 56/99: RUN --mount=type=bind,from=container-nesting,source=/,target=/ctx sh -c 'exec \"$SH\"'\n"
	const tagged = "Successfully tagged ghcr.io/opencharly/githubrunner:check-githubrunner-pod-2026.254.2125\n"

	t.Run("the nested podman advisory is exempt once the image is tagged", func(t *testing.T) {
		d := scanStepDiagnostics(step + advisory + "Trying to pull quay.io/libpod/alpine:latest...\n" + tagged)
		if d.Warnings != 0 || d.Allowlisted != 1 {
			t.Errorf("the container-inherent advisory must be exempt; got %+v", d)
		}
		if id := allowIDForLine(d, advisory); id != "podman-nested-rootfs-not-shared-mount" {
			t.Errorf("want the podman-nested-rootfs-not-shared-mount entry, got %q", id)
		}
	})

	t.Run("the advisory in a build that never tags is still a warning", func(t *testing.T) {
		d := scanStepDiagnostics(step + advisory)
		if d.Warnings != 1 || d.Allowlisted != 0 {
			t.Errorf("an untagged build must not discharge the advisory; got %+v", d)
		}
	})

	t.Run("other podman logrus warnings are untouched", func(t *testing.T) {
		const other = `time="2026-09-11T21:31:45Z" level=warning msg="some other podman warning"` + "\n"
		d := scanStepDiagnostics(step + other + tagged)
		if d.Warnings != 1 || d.Allowlisted != 0 {
			t.Errorf("the entry must claim ONLY the shared-mount advisory; got %+v", d)
		}
	})
}

// allowIDForLine returns the allowlist id a scan attributed to the finding whose text equals
// line, or "" when the line carried no exemption. Naming WHICH entry claimed a line is the
// whole point of the per-entry conditional form, so the tests assert it rather than the count
// alone.
func allowIDForLine(d stepDiagnostics, line string) string {
	for _, f := range d.Findings {
		if f.Text == strings.TrimSpace(line) {
			return f.AllowID
		}
	}
	return ""
}

// TestSystemdUnitFileDaemonReloadAllowanceIsScoped proves the systemd 'unit file
// changed on disk' notice (printed by RPM scriptlets when a package ships/modifies
// a unit — nfs-utils/gssproxy etc.) is allowlisted, while a REAL unit-file error
// (a warning that does mean something went wrong) is not claimed.
func TestSystemdUnitFileDaemonReloadAllowanceIsScoped(t *testing.T) {
	claimed := []string{
		">>> Warning: The unit file, source configuration file or drop-ins of gssproxy.se",
		">>> Warning: The unit file, source configuration file or drop-ins of nfs-utils.service changed on disk. Run 'systemctl daemon-reload' to reload.",
	}
	for _, line := range claimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", line)
		}
		a := allowanceFor(sev, line)
		if a == nil || a.ID != "systemd-unit-file-daemon-reload" {
			t.Errorf("%q: want the systemd-unit-file allowance, got %v", line, a)
		}
	}

	notClaimed := []string{
		">>> Warning: Failed to connect to bus: No such file or directory",
		">>> Warning: systemd-machine-id-setup failed: no machine ID found",
		">>> Warning: unit file gssproxy.service could not be loaded (not a valid unit)",
	}
	for _, line := range notClaimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			continue // not recognised as a diagnostic; nothing to exempt
		}
		if a := allowanceFor(sev, line); a != nil && a.ID == "systemd-unit-file-daemon-reload" {
			t.Errorf("%q must NOT be claimed by the systemd-unit-file allowance", line)
		}
	}
}

// TestScanCountsAllowlistedSeparately proves an exempted line is REPORTED, not erased. A gate that
// deleted its exemptions would read "0 warnings" while suppressing eight, which is the failure
// mode the summary's separate Allowlisted count exists to prevent.
func TestScanCountsAllowlistedSeparately(t *testing.T) {
	log := "STEP 1/2: RUN pacman -Syu --noconfirm --needed dbus\n" +
		"warning: dbus-1.16.2-1.1 is up to date -- skipping\n" +
		"STEP 2/2: RUN something-else\n" +
		"warning: this one is not exempt at all\n"

	d := scanStepDiagnostics(log)
	if d.Allowlisted != 1 {
		t.Errorf("Allowlisted = %d, want 1", d.Allowlisted)
	}
	if d.Warnings != 1 {
		t.Errorf("Warnings = %d, want 1 (the non-exempt line only)", d.Warnings)
	}
	if d.Errors != 0 {
		t.Errorf("Errors = %d, want 0", d.Errors)
	}
	var exempted int
	for _, f := range d.Findings {
		if f.AllowID != "" {
			exempted++
		}
	}
	if exempted != 1 {
		t.Errorf("findings carrying an AllowID = %d, want 1 — an exemption must stay auditable", exempted)
	}
}

// TestPromotedWarningTierGoesRed and TestWarningTierIsReportedEvenWhenNotFatal are the two
// assertions the file header cites as what keeps the warning stage from being a permanent
// weakening. They were named there before they were written — review caught the citation pointing
// at nothing, which is worse than an uncovered stage, because it asserts the stage is safe on
// evidence that does not exist.

// TestPromotedWarningTierGoesRed proves the promotion is a FLAG FLIP and not a new feature: the
// same scan result that passes today fails the moment WarningsFatal is true, and the failure
// message names the warning rather than reporting a generic red. The log is a real captured shape
// — a pacman skew line the allowlist does NOT claim — so this cannot pass on a synthetic string
// the matcher was written around.
func TestPromotedWarningTierGoesRed(t *testing.T) {
	const log = "STEP 1/2: RUN pacman -Syu --noconfirm --needed some-package\n" +
		"warning: could not fully load metadata for package some-package-1.0-1\n" +
		"STEP 2/2: RUN true\n"

	d := scanStepDiagnostics(log)
	if d.Warnings != 1 || d.Errors != 0 {
		t.Fatalf("fixture must produce exactly one non-allowlisted warning and no error; got %+v", d)
	}

	staged := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: false}
	if d.fails(staged) {
		t.Errorf("the STAGED policy must not fail on a warning — that is what makes it staged")
	}
	if got := defaultDiagnosticPolicy(); got != staged {
		t.Errorf("defaultDiagnosticPolicy() = %+v, want %+v — the header claims the stage is one field", got, staged)
	}

	promoted := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}
	if !d.fails(promoted) {
		t.Errorf("flipping WarningsFatal must turn this log red; it did not")
	}
	msg := d.failure(promoted, "image-build", ".check/x/y/image-build.log")
	if msg == "" || !strings.Contains(msg, "warning") {
		t.Errorf("the promoted failure must name the warning tier; got %q", msg)
	}

	// The allowlist must survive promotion: an EXEMPTED warning stays exempt when the tier is
	// fatal, or promotion would silently invalidate every reviewed exemption at once.
	exempt := scanStepDiagnostics("STEP 1/1: RUN pacman -Syu --needed dbus\n" +
		"warning: dbus-1.16.2-1.1 is up to date -- skipping\n")
	if exempt.Allowlisted != 1 || exempt.Warnings != 0 {
		t.Fatalf("fixture must be fully allowlisted; got %+v", exempt)
	}
	if exempt.fails(promoted) {
		t.Errorf("an allowlisted warning must not fail even under the promoted policy")
	}
}

// TestWarningTierIsReportedEvenWhenNotFatal proves the count is never silently dropped while the
// tier is staged off. A gate that stopped REPORTING what it stopped FAILING on would be a
// weakening dressed as a stage — the operator would have no way to see the debt accumulating, and
// the promotion condition in the header could never be evaluated.
func TestWarningTierIsReportedEvenWhenNotFatal(t *testing.T) {
	const log = "STEP 1/2: RUN pacman -Syu --noconfirm --needed dbus other\n" +
		"warning: dbus-1.16.2-1.1 is up to date -- skipping\n" +
		"warning: could not fully load metadata for package other-1.0-1\n" +
		"STEP 2/2: RUN true\n"

	d := scanStepDiagnostics(log)
	staged := defaultDiagnosticPolicy()
	if d.fails(staged) {
		t.Fatalf("the staged policy must not fail here; got %+v", d)
	}

	// Counted, not erased — both tiers, separately.
	if d.Warnings != 1 {
		t.Errorf("Warnings = %d, want 1 (the non-exempt line)", d.Warnings)
	}
	if d.Allowlisted != 1 {
		t.Errorf("Allowlisted = %d, want 1 (the exempt line)", d.Allowlisted)
	}

	// And REPORTED: the per-run notice a reader actually sees must mention the warning even
	// though nothing failed. This is the half that makes the stage auditable.
	notice := diagNotice(d)
	if notice == "" || !strings.Contains(notice, "warning") {
		t.Errorf("a non-fatal warning must still appear in the run notice; got %q", notice)
	}

	// The shape report must carry it too, so the promotion condition can be evaluated from a
	// real run rather than from a count alone.
	var shapes []string
	for _, sh := range d.shapes() {
		shapes = append(shapes, sh.Text)
	}
	joined := strings.Join(shapes, "\n")
	if !strings.Contains(joined, "could not fully load metadata") {
		t.Errorf("the non-fatal warning is missing from the shape report:\n%s", joined)
	}
}

// TestMkinitcpioChrootWarningsAllowanceIsScoped proves the four chroot-artifact
// warnings (sd-vconsole default, os-prober skip, fsck helpers absent, and the
// aggregate errors-encountered summary) are allowlisted while a REAL mkinitcpio
// warning is not.
func TestMkinitcpioChrootWarningsAllowanceIsScoped(t *testing.T) {
	claimed := []string{
		"==> WARNING: sd-vconsole: \"/etc/vconsole.conf\" not found, will use default values",
		"Warning: os-prober will not be executed to detect other bootable partitions.",
		"==> WARNING: No fsck helpers found. fsck will not be run on boot.",
		"==> WARNING: errors were encountered during the build. The image may not be complete.",
	}
	for _, line := range claimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", line)
		}
		a := allowanceFor(sev, line)
		if a == nil || a.ID != "mkinitcpio-chroot-warnings" {
			t.Errorf("%q: want the mkinitcpio-chroot-warnings allowance, got %v", line, a)
		}
	}

	notClaimed := []string{
		"==> WARNING: missing kernel module for root device",
		"Warning: grub-install failed to embed a core image",
	}
	for _, line := range notClaimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			continue // not recognised as a diagnostic; nothing to exempt
		}
		if a := allowanceFor(sev, line); a != nil && a.ID == "mkinitcpio-chroot-warnings" {
			t.Errorf("%q must NOT be claimed by the mkinitcpio-chroot-warnings allowance", line)
		}
	}
}

func TestPacmanPacnewConfigNoticeAllowanceIsScoped(t *testing.T) {
	claimed := []string{
		"warning: /etc/locale.gen installed as /etc/locale.gen.pacnew",
		"warning: /etc/tpm2-tss/fapi-profiles/P_ECCP384SHA384.json installed as /etc/tpm2-tss/fapi-profiles/P_ECCP384SHA384.json.pacnew",
		"warning: /etc/tpm2-tss/fapi-profiles/P_RSA3072SHA384.json installed as /etc/tpm2-tss/fapi-profiles/P_RSA3072SHA384.json.pacnew",
	}
	for _, line := range claimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", line)
		}
		a := allowanceFor(sev, line)
		if a == nil || a.ID != "pacman-pacnew-config-notice" {
			t.Errorf("%q: want the pacman-pacnew-config-notice allowance, got %v", line, a)
		}
	}

	notClaimed := []string{
		"warning: iproute2-7.2.0-1 is up to date -- skipping",
		"warning: zstd: local (7.1) is newer than repo (7.0)",
	}
	for _, line := range notClaimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			continue
		}
		if a := allowanceFor(sev, line); a != nil && a.ID == "pacman-pacnew-config-notice" {
			t.Errorf("%q must NOT be claimed by the pacman-pacnew-config-notice allowance", line)
		}
	}
}

// TestUpdateRcDAllowanceIsScoped covers the debootstrap update-rc.d allowance and its
// BOUNDARY: the pattern must claim ONLY that exact sentence. The negative cases are real
// update-rc.d lines that share its opening words but are NOT the chroot fallback notice.
func TestUpdateRcDAllowanceIsScoped(t *testing.T) {
	claimed := []string{
		"update-rc.d: warning: start and stop actions are no longer supported; falling back to defaults",
	}
	for _, line := range claimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", line)
		}
		a := allowanceFor(sev, line)
		if a == nil || a.ID != "update-rc-d-chroot-fallback" {
			t.Errorf("%q: want the update-rc.d allowance, got %v", line, a)
		}
	}

	notClaimed := []string{
		"update-rc.d: warning: /etc/init.d/foo exists but is not executable",
		"update-rc.d: error: cannot find a LSB script for foo",
		"update-rc.d: warning: start and stop actions are no longer supported; falling back to defaults and something else",
	}
	for _, line := range notClaimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			continue // not recognised as a diagnostic; nothing to exempt
		}
		if a := allowanceFor(sev, line); a != nil && a.ID == "update-rc-d-chroot-fallback" {
			t.Errorf("%q: the update-rc.d allowance must not claim this line", line)
		}
	}
}

// The snapshot-alignment downgrade entry, exercised on the EXACT lines a real Omarchy build
// emits. layer-omarchy-base repoints pacman at Omarchy's frozen mirror and runs -Syuu so the
// image IS that snapshot rather than a mix of two; these five lines are that alignment doing
// its job, and a build producing none of them would mean it had not run.
func TestSnapshotDowngradeIsAllowlisted(t *testing.T) {
	log := "STEP 3/9: RUN pacman -Syuu --noconfirm\n" +
		"warning: ca-certificates-mozilla: downgrading from version 3.128-1 to version 3.127-1\n" +
		"warning: libcap-ng: downgrading from version 0.9.5-1 to version 0.9.3-1\n" +
		"warning: libgcrypt: downgrading from version 1.12.3-1 to version 1.12.2-1\n" +
		"warning: libksba: downgrading from version 1.8.1-1 to version 1.8.0-1\n" +
		"warning: openssl: downgrading from version 3.6.4-1 to version 3.6.3-1\n"

	d := scanStepDiagnostics(log)
	if d.Allowlisted != 5 {
		t.Errorf("Allowlisted = %d, want 5", d.Allowlisted)
	}
	if d.Warnings != 0 {
		t.Errorf("Warnings = %d, want 0 — every line is the intended alignment", d.Warnings)
	}
	for _, f := range d.Findings {
		if f.AllowID != "pacman-repo-serves-older-than-installed" {
			t.Errorf("finding %q claimed by %q, want pacman-repo-serves-older-than-installed", f.Text, f.AllowID)
		}
	}
}

// The entry must stay NARROW. It claims the per-package downgrade sentence and nothing else:
// not an epoch-versioned variant it was never scoped for, not a failed transaction, and not
// a line that merely contains the word.
func TestSnapshotDowngradeEntryDoesNotOverClaim(t *testing.T) {
	// An epoch on either side is still the same sentence and must be claimed.
	claimed := scanStepDiagnostics("warning: mesa: downgrading from version 1:26.2.1-1 to version 1:26.2.0-1\n")
	if claimed.Allowlisted != 1 || claimed.Warnings != 0 {
		t.Errorf("an epoch-versioned downgrade must be claimed: allowlisted=%d warnings=%d",
			claimed.Allowlisted, claimed.Warnings)
	}

	// These must NOT be claimed — each is a different event that still deserves attention.
	for _, line := range []string{
		"error: failed to prepare transaction (could not satisfy dependencies)\n",
		"warning: downgrading is dangerous and you should not do it\n",
		"warning: openssl: downgrading from version 3.6.4-1 to version 3.6.3-1 and then exploding\n",
	} {
		d := scanStepDiagnostics(line)
		for _, f := range d.Findings {
			if f.AllowID == "pacman-repo-serves-older-than-installed" {
				t.Errorf("entry over-claimed %q", line)
			}
		}
	}
}

// TestPacmanMirrorAbandonedTransactionAllowance covers the entry added for pacman's own summary
// sentence when it gives up on ONE mirror for the remainder of a transaction:
// `warning: too many errors from <host>, skipping for the remainder of this transaction`.
//
// It is the COMPANION of pacman-mirror-retrieval-recovered: that entry claims the per-file
// `error: failed retrieving file …` lines CONDITIONALLY on the package installing, and this one
// claims the sentence that summarizes them, CONDITIONALLY on pacman reaching its keyring check.
// The defect it closes is a summary with no entry at all: a cold-cache cachyos image-build
// exited 0 with 0 errors and 1 warning, that one warning being this line, so R10's zero-warning
// bar was unreachable on every run whose mirror served 404s.
func TestPacmanMirrorAbandonedTransactionAllowance(t *testing.T) {
	const id = "pacman-mirror-abandoned-transaction-recovered"
	// The REAL sentence, captured verbatim at image-build.log line 1461 of the cold-cache
	// check-cachyos-immich-ml-pod run (calver 2026.255.0001), whose summary.yml reported
	// warnings: 1 with this line as the only un-allowlisted finding.
	const sentence = "warning: too many errors from cdn77.cachyos.org, skipping for the remainder of this transaction"
	const step = "STEP 1/12: RUN pacman -Syu --noconfirm --needed\n"
	const keyring = "checking keyring...\n"

	// The tier is part of the contract: this is pacman's WARNING-tier fallback notice, and an
	// entry that widened the ERROR tier to claim it would be a different, unreviewed change.
	t.Run("classifies as a warning and is claimed", func(t *testing.T) {
		sev, _, ok := classifyDiagnosticLine(sentence)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", sentence)
		}
		if sev != severityWarning {
			t.Fatalf("the sentence classifies as %q, want %q", sev, severityWarning)
		}
		a := allowanceFor(sev, sentence)
		if a == nil || a.ID != id {
			t.Fatalf("%q: want the %s allowance, got %v", sentence, id, a)
		}
		if a.Severity != severityWarning {
			t.Errorf("entry severity = %q, want %q", a.Severity, severityWarning)
		}
	})

	// The verbatim shape of the captured run: the per-file 404s from one CDN, the sentence,
	// then the keyring/integrity stages and the upgrades that prove the packages arrived.
	t.Run("the real cold-build sequence scans clean", func(t *testing.T) {
		d := scanStepDiagnostics(step +
			"error: failed retrieving file 'glibc-2.44+r24+g16be1518495f-1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404\n" +
			"error: failed retrieving file 'gcc-16.2.1+r23+gd564253eb6c8-1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404\n" +
			"error: failed retrieving file 'libgfortran-16.2.1+r23+gd564253eb6c8-1-x86_64_v3.pkg.tar.zst' from cdn77.cachyos.org : The requested URL returned error: 404\n" +
			sentence + "\n" + keyring +
			"checking package integrity...\n" +
			"upgrading glibc...\n" + "upgrading gcc...\n" + "upgrading libgfortran...\n")
		if d.Errors != 0 || d.Warnings != 0 || d.Allowlisted != 4 {
			t.Errorf("the captured sequence must scan clean: errors=%d warnings=%d allowlisted=%d "+
				"(want 0/0/4); got %+v", d.Errors, d.Warnings, d.Allowlisted, d)
		}
	})

	// The conditional half: without pacman's keyring stage the sentence is NOT claimed — and
	// when the warning tier is promoted that unrecovered abandonment goes red.
	t.Run("no recovery is not exempted", func(t *testing.T) {
		d := scanStepDiagnostics(step + sentence + "\n")
		if d.Warnings != 1 || d.Allowlisted != 0 {
			t.Errorf("without pacman's keyring stage the sentence must not be claimed; got %+v", d)
		}
		promoted := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}
		if !d.fails(promoted) {
			t.Errorf("an unrecovered abandonment must go red under the promoted warning tier; got %+v", d)
		}
	})

	// The sentence cannot hide a failure: a transaction that never retrieved its files exits
	// nonzero and prints its OWN error line, which is error-tier and fatal whatever this entry
	// claims about the fallback notice above it.
	t.Run("a genuinely failed transaction is still fatal", func(t *testing.T) {
		d := scanStepDiagnostics(step + sentence + "\n" +
			"error: failed to commit transaction (failed to retrieve some files)\n")
		if d.Errors != 1 || !d.fails(defaultDiagnosticPolicy()) {
			t.Errorf("a transaction that cannot retrieve its files must still fail the step; got %+v", d)
		}
	})

	// The entry must stay NARROW: it claims the EXACT sentence and nothing near it. Every row
	// below carries the keyring recovery, so a claim could only come from the pattern — never
	// from an absent proof.
	for _, line := range []string{
		"warning: too many errors from cdn77.cachyos.org, skipping for the remainder of this transaction and then exploding",
		"warning: too many errors from cdn77.cachyos.org, skipping for the rest of this transaction",
		"warning: too many errors from",
		"warning: failed to retrieve some files",
		"error: too many errors from cdn77.cachyos.org, skipping for the remainder of this transaction",
	} {
		t.Run("near miss: "+line, func(t *testing.T) {
			d := scanStepDiagnostics(step + line + "\n" + keyring)
			for _, f := range d.Findings {
				if f.AllowID == id {
					t.Errorf("the entry over-claimed %q", line)
				}
			}
			if d.Allowlisted != 0 {
				t.Errorf("%q must not be claimed by any entry; got %+v", line, d)
			}
		})
	}
}

// TestPacmanReinstalledAlreadyCurrentAllowanceIsConditional covers the sibling of
// pacman-needed-package-already-current: the SAME pacman sentence with the verb pacman prints
// when `--needed` was NOT passed, so the package is REINSTALLED rather than skipped. Only that
// verb separates the two, which is why they are two entries instead of one widened alternation —
// and why the boundary between them is asserted here rather than left to review.
//
// The entry is CONDITIONAL on pacman's own `reinstalling <name>...` line, so the negative half is
// the point: a `-- reinstalling` warning with no matching reinstall line stays counted.
func TestPacmanReinstalledAlreadyCurrentAllowanceIsConditional(t *testing.T) {
	const id = "pacman-package-reinstalled-already-current"
	const step = "STEP 1/12: RUN pacman -S --noconfirm --cachedir /tmp/repro-cache glibc gcc\n"

	// The lines a real transaction prints, verbatim: the warning names <name>-<pkgver>-<pkgrel>
	// (an EPOCH renders as <name>-<epoch>:<ver>-<rel>), the progress line names the package
	// alone. Each row exercises a shape the capture must survive.
	pkgs := []struct{ warning, recovery string }{
		{"warning: glibc-2.44+r24+g16be1518495f-1 is up to date -- reinstalling", "reinstalling glibc..."},
		{"warning: libgfortran-16.2.1+r23+gd564253eb6c8-1 is up to date -- reinstalling", "reinstalling libgfortran..."},
		{"warning: rust-1:1.98.0-1.1 is up to date -- reinstalling", "reinstalling rust..."},
		{"warning: nvidia-container-toolkit-1.17.8-1 is up to date -- reinstalling", "reinstalling nvidia-container-toolkit..."},
	}

	t.Run("classifies as a warning and is claimed", func(t *testing.T) {
		sev, _, ok := classifyDiagnosticLine(pkgs[0].warning)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", pkgs[0].warning)
		}
		if sev != severityWarning {
			t.Fatalf("the sentence classifies as %q, want %q", sev, severityWarning)
		}
		a := allowanceFor(sev, pkgs[0].warning)
		if a == nil || a.ID != id {
			t.Fatalf("%q: want the %s allowance, got %v", pkgs[0].warning, id, a)
		}
	})

	// The captured sequence: every warned package followed by pacman's own reinstall line, so
	// the step scans clean — and every claimed line is claimed by THIS entry, not by a
	// neighbour that happens to match the same sentence.
	t.Run("the real sequence scans clean", func(t *testing.T) {
		var log strings.Builder
		log.WriteString(step)
		for _, p := range pkgs {
			log.WriteString(p.warning + "\n")
		}
		log.WriteString("checking keyring...\n")
		for _, p := range pkgs {
			log.WriteString(p.recovery + "\n")
		}
		d := scanStepDiagnostics(log.String())
		if d.Errors != 0 || d.Warnings != 0 || d.Allowlisted != len(pkgs) {
			t.Errorf("want 0 errors / 0 warnings / %d allowlisted; got %+v", len(pkgs), d)
		}
		for _, f := range d.Findings {
			if f.AllowID != "" && f.AllowID != id {
				t.Errorf("line claimed by %q, want %q: %q", f.AllowID, id, f.Text)
			}
		}
	})

	// The conditional half: the warning alone is pacman's plan, not pacman acting, so with no
	// reinstall line the entry claims nothing and the line stays a warning.
	t.Run("no reinstall line is not exempted", func(t *testing.T) {
		d := scanStepDiagnostics(step + pkgs[0].warning + "\nchecking keyring...\n")
		if d.Warnings != 1 || d.Allowlisted != 0 {
			t.Errorf("without pacman's reinstall line the warning must not be claimed; got %+v", d)
		}
		promoted := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}
		if !d.fails(promoted) {
			t.Errorf("an unrecovered reinstall warning must go red under the promoted tier; got %+v", d)
		}
	})

	// The subject tie: another package's reinstall line must NOT discharge this package's
	// warning, which is the property the capture group exists for.
	t.Run("another package's reinstall line does not discharge it", func(t *testing.T) {
		d := scanStepDiagnostics(step + pkgs[0].warning + "\nreinstalling gcc...\n")
		if d.Warnings != 1 || d.Allowlisted != 0 {
			t.Errorf("a different package's reinstall line must not claim this warning; got %+v", d)
		}
	})

	// The entry must stay NARROW. Every row below carries a reinstall line, so a claim could
	// only come from the pattern — never from an absent proof.
	for _, line := range []string{
		// The sibling VERB belongs to the sibling entry, not to this one.
		"warning: glibc-2.44+r24+g16be1518495f-1 is up to date -- skipping",
		// No <name>-<ver>-<rel> token: two hyphen-separated trailing fields are required.
		"warning: glibc is up to date -- reinstalling",
		// The sentence must END there.
		"warning: glibc-2.44+r24+g16be1518495f-1 is up to date -- reinstalling and then exploding",
		// A warning-tier entry can never absolve an error-tier finding.
		"error: glibc-2.44+r24+g16be1518495f-1 is up to date -- reinstalling",
	} {
		t.Run("near miss: "+line, func(t *testing.T) {
			d := scanStepDiagnostics(step + line + "\nreinstalling glibc...\n")
			for _, f := range d.Findings {
				if f.AllowID == id {
					t.Errorf("the entry over-claimed %q", line)
				}
			}
		})
	}
}

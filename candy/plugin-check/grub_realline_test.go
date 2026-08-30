package check

import "testing"

// The REAL grub-probe line, captured byte-for-byte from a cold debian-coder image
// build (od -c): the opening quote is a BACKTICK (0x60), the closing one a straight
// apostrophe, the subject is `overlay` (not fuse-overlayfs), and the line ends CRLF.
//
// This is the line the entry has to claim in production. An earlier version of the
// pattern was anchored on '...' and matched NOTHING here — the step still failed with
// errors=1, allowlisted=0 — which is why this test carries the captured bytes rather
// than a hand-typed approximation of them.
const realGrubLine = "/usr/sbin/grub-probe: error: failed to get canonical path of \x60overlay'.\r\n"

const realGrubStep = "STEP 1/1: RUN apt-get install -y grub-common\n"

func TestGrubProbe_RealCapturedLine_Recovered(t *testing.T) {
	tagged := "Successfully tagged ghcr.io/opencharly/debian-coder:2026.242.0853\n"

	d := scanStepDiagnostics(realGrubStep + realGrubLine + tagged)
	if d.Errors != 0 || d.Allowlisted != 1 || d.fails(defaultDiagnosticPolicy()) {
		t.Errorf("a completed build must exempt the REAL grub-probe line; got %+v", d)
	}
}

// The conditional invariant, proved against the real bytes rather than only the
// synthetic ones: with no `Successfully tagged` recovery in the same log, the entry
// must NOT claim the line and the step must still fail.
func TestGrubProbe_RealCapturedLine_NoRecoveryIsFatal(t *testing.T) {
	d := scanStepDiagnostics(realGrubStep + realGrubLine)
	if d.Errors != 1 || !d.fails(defaultDiagnosticPolicy()) {
		t.Errorf("without the image tag the REAL grub-probe line must stay fatal; got %+v", d)
	}
}

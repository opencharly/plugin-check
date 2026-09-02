package check

import "testing"

// recordings_test.go covers the recordings-manifest parser (the deterministic
// core); the survival/host-side behavior is exercised by the R10 bed.

func TestScanRecordingsLog(t *testing.T) {
	body := `PASS check the recording stops ... [run-stop] Recording stopped (mode: terminal); saved 914 bytes to /tmp/spike-record-run.cast
PASS check SPICE ... [srec-stop] Recording stopped (name: vm-walk, frames: 1, bytes: 112049)
SOME OTHER LINE`
	got := scanRecordingsLog(body)
	if len(got) != 2 {
		t.Fatalf("scanRecordingsLog = %d entries, want 2", len(got))
	}
	cast := got[0]
	if cast.Mode != "terminal" || cast.Bytes != 914 || cast.Artifact != "/tmp/spike-record-run.cast" {
		t.Errorf("cast entry wrong: %+v", cast)
	}
	mjpeg := got[1]
	if mjpeg.Mode != "desktop-mjpeg" || mjpeg.Bytes != 112049 || mjpeg.Frames != 1 || mjpeg.Name != "vm-walk" {
		t.Errorf("mjpeg entry wrong: %+v", mjpeg)
	}
}

func TestScanRecordingsLogEmpty(t *testing.T) {
	if got := scanRecordingsLog("no record steps here\n"); len(got) != 0 {
		t.Errorf("empty scan = %d entries, want 0", len(got))
	}
}

func TestAtoi(t *testing.T) {
	for s, want := range map[string]int{"0": 0, "914": 914, "112049": 112049} {
		if got := atoi(s); got != want {
			t.Errorf("atoi(%q) = %d, want %d", s, got, want)
		}
	}
}

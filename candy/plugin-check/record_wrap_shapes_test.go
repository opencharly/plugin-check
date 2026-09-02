package check

import (
	"bytes"
	"os"
	"testing"
)

func TestDeployRecordWrapShapes(t *testing.T) {
	// Canonical shape: the bed root FleetNode (spec.Deploy) JSON carries record:
	// at the TOP level of the node (the deploy spec IS the node).
	topLevel := []byte(`{
	  "name": "check-bed",
	  "record": {"terminal": true, "record_name": "whole-run", "fps": 4},
	  "disposable": true
	}`)
	rw, ok := deployRecordWrap(topLevel)
	if !ok {
		t.Fatal("top-level record not extracted")
	}
	if rw["record_name"] != "whole-run" {
		t.Errorf("record_name = %v", rw["record_name"])
	}
	if rw["fps"] != 4 {
		t.Errorf("fps = %v, want 4", rw["fps"])
	}

	// Envelope shape: a wrapper map may carry the deploy spec under a deploy key.
	envelope := []byte(`{
	  "name": "check-bed",
	  "deploy": {"record": {"terminal": false, "desktop": true}}
	}`)
	rw2, ok2 := deployRecordWrap(envelope)
	if !ok2 {
		t.Fatal("envelope record not extracted")
	}
	if rw2["desktop"] != true {
		t.Errorf("desktop = %v, want true", rw2["desktop"])
	}

	// Negative: no record block -> (nil, false).
	none := []byte(`{"name": "check-bed", "disposable": true}`)
	if _, ok3 := deployRecordWrap(none); ok3 {
		t.Fatal("record extracted from node without record block")
	}
}

func TestDeployRecordWrapRecordSteps(t *testing.T) {
	rw, _ := deployRecordWrap([]byte(`{"record": {"terminal": true, "record_name": "whole-run"}}`))
	start, stop, err := recordWrapSteps(t.TempDir(), rw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(start)
	if !bytes.Contains(b, []byte("record-wrap-start")) {
		t.Error("start step missing wrap id")
	}
	if !bytes.Contains(b, []byte("whole-run")) {
		t.Error("start step missing record_name")
	}
	b2, _ := os.ReadFile(stop)
	if !bytes.Contains(b2, []byte("record-wrap-stop")) {
		t.Error("stop step missing wrap id")
	}
	if !bytes.Contains(b2, []byte("whole-run.cast")) {
		t.Error("stop step missing artifact path")
	}
}

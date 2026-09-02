package check

import (
	"bytes"
	"os"
	"testing"
)

func TestWrapStepsPluginDiscriminator(t *testing.T) {
	rw, _ := deployRecordWrap([]byte(`{"record": {"terminal": true, "record_name": "whole-run"}}`))
	start, stop, err := recordWrapSteps(t.TempDir(), rw)
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"start": start, "stop": stop} {
		b, _ := os.ReadFile(p)
		if !bytes.Contains(b, []byte("plugin: record")) {
			t.Errorf("%s step missing plugin: record discriminator:\n%s", name, b)
		}
		if !bytes.Contains(b, []byte("whole-run")) {
			t.Errorf("%s step missing record_name in plugin_input", name)
		}
	}
}

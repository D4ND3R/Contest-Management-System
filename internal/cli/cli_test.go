package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "cms ") {
		t.Fatalf("version: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := Main(nil, &out, &errb); code != 2 {
		t.Fatalf("no args: code=%d", code)
	}
	if code := Main([]string{"nope"}, &out, &errb); code != 2 {
		t.Fatalf("unknown: code=%d", code)
	}
	out.Reset()
	if code := Main([]string{"help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "contest-web") {
		t.Fatalf("help: %q", out.String())
	}
}

func TestCtlUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"ctl", "frobnicate"}, &out, &errb); code != 2 {
		t.Fatalf("code = %d", code)
	}
	out.Reset()
	if code := Main([]string{"ctl", "help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "migrate") {
		t.Fatalf("ctl help: %q", out.String())
	}
}

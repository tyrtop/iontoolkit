package ioncli

import (
	"strings"
	"testing"
)

func TestIsReadOnly(t *testing.T) {
	cases := map[string]bool{
		"dump interface status 1":             true,
		"dump device status":                  true,
		"inspect system arp interface=vlan96": true,
		"  dump   device status  ":            true,
		"DUMP device status":                  true,
		"ping 8.8.8.8":                        true,
		"traceroute6 2001:db8::1":             true,
		"help":                                true,
		"config banner hello":                 false,
		"set interface 1 enabled=true":        false,
		"clear flow all":                      false,
		"debug ave create app-detection":      false,
		"file remove /tmp/x":                  false,
		"exit":                                false,
		"tcpdump -i eth0":                     false,
		"ssh user@host":                       false,
		"dump-support":                        false,
		"curl http://x -X POST":               false,
		"":                                    false,
		"dumpfoo":                             false,
	}

	for cmd, want := range cases {
		t.Run(cmd, func(t *testing.T) {
			if got := IsReadOnly(cmd); got != want {
				t.Errorf("IsReadOnly(%q) = %v, want %v", cmd, got, want)
			}
		})
	}
}

func TestVerbExtraction(t *testing.T) {
	cases := map[string]string{
		"dump device status": "dump",
		"  DUMP  x ":         "dump",
		"ping6":              "ping6",
		"":                   "",
		"   ":                "",
	}
	for in, want := range cases {
		if got := Verb(in); got != want {
			t.Errorf("Verb(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckReadOnly(t *testing.T) {
	if err := CheckReadOnly([]string{"dump device status", "inspect system arp"}); err != nil {
		t.Errorf("all read-only returned %v", err)
	}

	err := CheckReadOnly([]string{"dump device status", "config banner x", "set y"})
	if err == nil {
		t.Fatal("want an error naming the write commands")
	}
	msg := err.Error()
	if !strings.Contains(msg, "config banner x") || !strings.Contains(msg, "set y") {
		t.Errorf("error should name both offenders: %q", msg)
	}
	if strings.Contains(msg, "dump device status") {
		t.Errorf("error should not name the safe command: %q", msg)
	}
	if !strings.Contains(msg, "commands not read-only") {
		t.Errorf("want plural wording: %q", msg)
	}

	if err := CheckReadOnly(nil); err != nil {
		t.Errorf("empty list returned %v", err)
	}
}

func TestReadOnlyVerbsSorted(t *testing.T) {
	v := ReadOnlyVerbs()
	if len(v) != len(readOnlyVerbs) {
		t.Fatalf("got %d verbs, want %d", len(v), len(readOnlyVerbs))
	}
	for i := 1; i < len(v); i++ {
		if v[i-1] > v[i] {
			t.Fatalf("not sorted at %d: %q > %q", i, v[i-1], v[i])
		}
	}
}

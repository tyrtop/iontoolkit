package main

import (
	"strings"
	"testing"
)

func TestWriteFlagsBecomeOneCall(t *testing.T) {
	args, rest := writeCall("interface", []string{"e1", "e2"}, []string{"1", "used_for=lan"}, []string{"description=a=b", "mtu=1500"})
	if args["what"] != "interface" || args["elements"] != "e1,e2" || args["match"] != "1;used_for=lan" {
		t.Errorf("args = %v", args)
	}
	if strings.Join(rest, "|") != "description=a=b|mtu=1500" {
		t.Errorf("rest = %v", rest)
	}
}

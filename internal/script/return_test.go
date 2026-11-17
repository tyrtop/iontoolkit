package script

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWhatAScriptReturnsReachesTheCaller(t *testing.T) {
	s := set(t, map[string]string{
		"data.lua": `return function(ctx) return { down = 192.69, ok = true, gone = ctx.null, ports = { "3", "4" } } end`,
		"none.lua": `return function(ctx) ctx.status("done") end`,
	})
	for _, tt := range []struct {
		script, want string
	}{
		{"data", `{"down":192.69,"gone":null,"ok":true,"ports":["3","4"]}`},
		{"none", "nothing"},
	} {
		got := "nothing"
		_, err := s.Run(context.Background(), Run{Name: tt.script, OnReturn: func(v any) {
			b, _ := json.Marshal(v)
			got = string(b)
		}})
		if err != nil || got != tt.want {
			t.Errorf("%s.lua returned %q, err %v; want %q", tt.script, got, err, tt.want)
		}
	}
}

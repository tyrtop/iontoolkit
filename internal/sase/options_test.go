package sase

import (
	"testing"
	"time"
)

func TestOptionsValidate(t *testing.T) {
	base := Options{
		Commands: []string{"dump device status"},
		Username: "u", Password: "p",
		Concurrency:    10,
		ElementTimeout: 60 * time.Second,
		SessionTimeout: 20 * time.Second,
		Attempts:       1,
	}

	cases := map[string]struct {
		mutate  func(*Options)
		wantErr bool
	}{
		"defaults": {func(o *Options) {}, false},
		"interactive needs no creds": {func(o *Options) {
			o.Commands, o.Username, o.Password = nil, "", ""
		}, false},
		"cmd without user":  {func(o *Options) { o.Username = "" }, true},
		"cmd without pass":  {func(o *Options) { o.Password = "" }, true},
		"zero concurrency":  {func(o *Options) { o.Concurrency = 0 }, true},
		"session > element": {func(o *Options) { o.SessionTimeout = 90 * time.Second }, true},
		"zero attempts":     {func(o *Options) { o.Attempts = 0 }, true},

		"3 attempts fits exactly": {func(o *Options) { o.Attempts = 3 }, false},
		"4 attempts overruns":     {func(o *Options) { o.Attempts = 4 }, true},
		"huge attempts overflows the check": {func(o *Options) {
			o.Attempts = 1 << 34
			o.SessionTimeout = time.Second
		}, true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			o := base
			tc.mutate(&o)
			err := o.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

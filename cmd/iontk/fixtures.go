package main

import (
	"fmt"
	"io"

	"tyrtop.com/iontk/internal/app"
)

func loadFixtures(w io.Writer) error {
	if flagFixtures == "" && !flagDemo {
		return nil
	}
	from, set, err := app.FakeFleet(app.OpenStore())
	if err != nil {
		return err
	}
	lines, err := app.UseFixtures(from, set)
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	return err
}

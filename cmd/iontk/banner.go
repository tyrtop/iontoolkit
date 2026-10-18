package main

import "strings"

var bannerRows = []string{
	" _                _    _    ",
	"(_)  ___   _ __  | |_ | | __",
	"| | / _ \\ | '_ \\ | __|| |/ /",
	"| || (_) || | | || |_ |   < ",
	"|_| \\___/ |_| |_| \\__||_|\\_\\",
}

const bannerWidth = 28

func bannerFits(width, height int) bool {
	return width >= bannerWidth && height >= 18
}

func bannerHeight(width, height int) int {
	if bannerFits(width, height) {
		return len(bannerRows) + 1
	}
	return 1
}

func banner(width, height int) string {
	if !bannerFits(width, height) {
		return styleCmd.Render("iontk") + "\n"
	}
	var b strings.Builder
	for _, r := range bannerRows {
		b.WriteString(styleCmd.Render(r) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

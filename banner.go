package main

import (
	"fmt"
	"io"
	"strings"
)

var bannerLines = []string{
	`███████╗███████╗██╗     ███████╗`,
	`██╔════╝██╔════╝██║     ██╔════╝`,
	`███████╗█████╗  ██║     █████╗`,
	`╚════██║██╔══╝  ██║     ██╔══╝`,
	`███████║███████╗███████╗██║`,
	`╚══════╝╚══════╝╚══════╝╚═╝`,
	``,
	`███╗   ███╗ ██████╗ ██████╗ ██╗███████╗██╗   ██╗██╗███╗   ██╗ ██████╗`,
	`████╗ ████║██╔═══██╗██╔══██╗██║██╔════╝╚██╗ ██╔╝██║████╗  ██║██╔════╝`,
	`██╔████╔██║██║   ██║██║  ██║██║█████╗   ╚████╔╝ ██║██╔██╗ ██║██║  ███╗`,
	`██║╚██╔╝██║██║   ██║██║  ██║██║██╔══╝    ╚██╔╝  ██║██║╚██╗██║██║   ██║`,
	`██║ ╚═╝ ██║╚██████╔╝██████╔╝██║██║        ██║   ██║██║ ╚████║╚██████╔╝`,
	`╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═╝╚═╝        ╚═╝   ╚═╝╚═╝  ╚═══╝ ╚═════╝`,
	``,
	` █████╗  ██████╗ ███████╗███╗   ██╗████████╗`,
	`██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝`,
	`███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║`,
	`██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║`,
	`██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║`,
	`╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝`,
}

var bannerSubtext = []string{
	"           Self  ·  Modifying  ·  Agent  ·  written in Go",
	"           v" + version,
}

type rgb struct{ r, g, b uint8 }

func lerpRGB(a, b rgb, t float64) rgb {
	return rgb{
		r: uint8(float64(a.r)*(1-t) + float64(b.r)*t),
		g: uint8(float64(a.g)*(1-t) + float64(b.g)*t),
		b: uint8(float64(a.b)*(1-t) + float64(b.b)*t),
	}
}

func ansiFg(c rgb) string {
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", c.r, c.g, c.b)
}

const ansiReset = "\033[0m"
const ansiBold = "\033[1m"

func printBanner(v string, w io.Writer) {
	start := rgb{56, 189, 248}
	end := rgb{217, 70, 239}

	lines := bannerLines
	total := len(lines)
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(w)
			continue
		}
		t := float64(i) / float64(total-1)
		c := lerpRGB(start, end, t)
		fmt.Fprintf(w, "%s%s%s%s\n", ansiBold, ansiFg(c), line, ansiReset)
	}
	for _, s := range bannerSubtext {
		fmt.Fprintf(w, "%s%s%s\n", ansiFg(rgb{148, 163, 184}), s, ansiReset)
	}
	fmt.Fprintln(w)
}

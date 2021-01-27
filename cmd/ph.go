// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package ph

import (
	"flag"
	"fmt"
	"os"

	"github.com/wabarc/telegra.ph/pkg"
)

func Run() {
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		e := os.Args[0]
		fmt.Printf("  %s url [url]\n\n", e)
		fmt.Printf("example:\n  %s https://www.eff.org/ https://www.fsf.org/\n\n", e)
		os.Exit(1)
	}

	wbrc := &ph.Archiver{}
	published, _ := wbrc.Wayback(args)
	for orig, dest := range published {
		fmt.Println(orig, "=>", dest)
	}
}

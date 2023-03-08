// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/wabarc/telegra.ph"
)

func main() {
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		e := os.Args[0]
		fmt.Printf("  %s url [url]\n\n", e)
		fmt.Printf("example:\n  %s https://www.eff.org/ https://www.fsf.org/\n\n", e)
		os.Exit(1)
	}

	wbrc := ph.New(nil)
	process(wbrc.Wayback, args)
}

func process(f func(context.Context, *url.URL) (string, error), args []string) {
	var wg sync.WaitGroup
	for _, arg := range args {
		wg.Add(1)
		go func(link string) {
			defer wg.Done()
			u, err := url.Parse(link)
			if err != nil {
				fmt.Println(link, "=>", fmt.Sprintf("%v", errors.WithStack(err)))
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			r, err := f(ctx, u)
			if err != nil {
				fmt.Println(link, "=>", fmt.Sprintf("%v", errors.WithStack(err)))
				return
			}
			fmt.Println(link, "=>", r)
		}(arg)
	}
	wg.Wait()
}

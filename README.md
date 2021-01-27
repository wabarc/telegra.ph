# A Golang and Command-Line Interface to Telegra.ph

This package is a command-line tool named `telegra.ph` convert webpage to [telegra.ph](https://telegra.ph), it also supports imports as a Golang package for a programmatic. Please report all bugs and issues on [Github](https://github.com/wabarc/telegra.ph/issues).

## Installation

From source:

```sh
$ go get github.com/wabarc/telegra.ph
```

From [gobinaries.com](https://gobinaries.com):

```sh
$ curl -sf https://gobinaries.com/wabarc/telegra.ph | sh
```

From [releases](https://github.com/wabarc/telegra.ph/releases)

## Usage

#### Command-line

```sh
$ telegra.ph https://www.eff.org/ https://www.fsf.org/

https://www.eff.org/ => https://telegra.ph/Electronic-Frontier-Foundation--Defending-your-rights-in-the-digital-world-01-27-5
https://www.fsf.org/ => https://telegra.ph/Front-Page--Free-Software-Foundation--working-together-for-free-software-01-27-2
```

#### Go package interfaces

```go
package main

import (
        "fmt"

        "github.com/wabarc/telegra.ph/pkg"
)

func main() {
        links := []string{"https://www.eff.org/", "https://www.fsf.org/"}
	wbrc := &ph.Archiver{}
	published, _ := wbrc.Wayback(links)
	for orig, dest := range published {
		fmt.Println(orig, "=>", dest)
	}
}

// Output:
// https://www.eff.org/ => https://telegra.ph/Electronic-Frontier-Foundation--Defending-your-rights-in-the-digital-world-01-27-5
// https://www.fsf.org/ => https://telegra.ph/Front-Page--Free-Software-Foundation--working-together-for-free-software-01-27-2
```

## License

This software is released under the terms of the GNU General Public License v3.0. See the [LICENSE](https://github.com/wabarc/telegra.ph/blob/main/LICENSE) file for details.


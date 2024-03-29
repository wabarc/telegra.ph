// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package ph // import "github.com/wabarc/telegra.ph"

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/wabarc/helper"
	"github.com/wabarc/screenshot"
)

// nolint:errcheck
func genImage() *os.File {
	width := 200
	height := 10000

	upLeft := image.Point{0, 0}
	lowRight := image.Point{width, height}

	img := image.NewRGBA(image.Rectangle{upLeft, lowRight})

	// Colors are defined by Red, Green, Blue, Alpha uint8 values.
	cyan := color.RGBA{100, 200, 200, 0xff}

	// Set color for each pixel.
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			switch {
			case x < width/2 && y < height/2: // upper left quadrant
				img.Set(x, y, cyan)
			case x >= width/2 && y >= height/2: // lower right quadrant
				img.Set(x, y, color.White)
			default:
				// Use zero value.
			}
		}
	}

	// Encode as PNG.
	f, _ := os.Create(os.TempDir() + "/image.png")
	png.Encode(f, img)

	return f
}

// nolint:errcheck
func writeHTML(content string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, strings.TrimSpace(content))
	})
}

func TestPost(t *testing.T) {
	f := genImage()
	defer os.Remove(f.Name())

	arc := &Archiver{}
	client, err := arc.newClient()
	if err != nil {
		t.Error(err)
	}
	arc.client = client
	sub := subject{title: []rune("testing"), source: "http://example.org"}

	dest, err := arc.post(sub, "", f.Name())
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(dest)
	if err != nil {
		t.Log("URL:", dest)
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		t.Fail()
	}
}

func TestWayback(t *testing.T) {
	binPath := helper.FindChromeExecPath()
	if _, err := exec.LookPath(binPath); err != nil {
		t.Skip("Chrome headless browser no found, skipped")
	}

	ts := httptest.NewServer(writeHTML(`
<html>
<head>
    <title>Example Domain</title>
</head>

<body>
<div>
    <h1>Example Domain</h1>
    <p>This domain is for use in illustrative examples in documents. You may use this
    domain in literature without prior coordination or asking for permission.</p>
    <p><a href="https://www.iana.org/domains/example">More information...</a></p>
</div>
</body>
</html>
	`))
	defer ts.Close()

	input, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	arc := &Archiver{}
	dst, err := arc.Wayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if dst == "" {
		t.Fatal("destination url empty")
	}

	resp, err := http.Get(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Unexpected wayback to telegra.ph, got status code %d instead of 200", resp.StatusCode)
	}
}

func TestWaybackByRemote(t *testing.T) {
	ts := httptest.NewServer(writeHTML(`
<html>
<head>
    <title>Example Domain</title>
</head>

<body>
<div>
    <h1>Example Domain</h1>
    <p>This domain is for use in illustrative examples in documents. You may use this
    domain in literature without prior coordination or asking for permission.</p>
    <p><a href="https://www.iana.org/domains/example">More information...</a></p>
</div>
</body>
</html>
	`))
	defer ts.Close()

	binPath := helper.FindChromeExecPath()
	if _, err := exec.LookPath(binPath); err != nil {
		t.Skip("Chrome headless browser no found, skipped")
	}

	host := "127.0.0.1"
	port := "9222"
	tempDir := t.TempDir()
	procCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(procCtx, binPath,
		"--no-first-run",
		"--no-default-browser-check",
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--user-data-dir="+tempDir,
		"--remote-debugging-address="+host,
		"--remote-debugging-port="+port,
		"about:blank",
	)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	input, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	arc := New(nil).ByRemote(net.JoinHostPort(host, port))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dst, err := arc.Wayback(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if dst == "" {
		t.Fatal("destination url empty")
	}

	resp, err := http.Get(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Unexpected wayback to telegra.ph by remote headless, got status code %d instead of 200", resp.StatusCode)
	}
}

func TestWaybackWithShots(t *testing.T) {
	binPath := helper.FindChromeExecPath()
	if _, err := exec.LookPath(binPath); err != nil {
		t.Skip("Chrome headless browser no found, skipped")
	}

	ts := httptest.NewServer(writeHTML(`
<html>
<head>
    <title>Example Domain</title>
</head>

<body>
<div>
    <h1>Example Domain</h1>
    <p>This domain is for use in illustrative examples in documents. You may use this
    domain in literature without prior coordination or asking for permission.</p>
    <p><a href="https://www.iana.org/domains/example">More information...</a></p>
</div>
</body>
</html>
	`))
	defer ts.Close()

	input, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	dirname, err := os.MkdirTemp(os.TempDir(), "telegraph")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dirname)

	arc := &Archiver{}
	ctx := context.Background()
	files := screenshot.Files{
		Image: path.Join(dirname, "image.png"),
		HTML:  path.Join(dirname, "html.html"),
	}
	shot, err := screenshot.Screenshot[screenshot.Path](ctx, input, screenshot.Quality(100), screenshot.AppendToFile(files))
	if err != nil {
		t.Fatal(err)
	}
	ctx = arc.WithShot(ctx, shot)
	dst, err := arc.Wayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if dst == "" {
		t.Fatal("destination url empty")
	}

	resp, err := http.Get(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Unexpected wayback to telegra.ph with shots, got status code %d instead of 200", resp.StatusCode)
	}
}

func TestSplitImage(t *testing.T) {
	file := genImage()
	defer os.Remove(file.Name())

	paths, err := splitImage(file.Name(), 8976)
	if err != nil {
		t.Log(err)
		t.Log(paths)
		t.Fail()
	}
}

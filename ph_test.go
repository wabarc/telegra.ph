// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package ph // import "github.com/wabarc/telegra.ph/pkg"

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
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
	arc.subject = subject{title: []rune("testing"), source: "http://example.org"}

	dest, err := arc.post("", f.Name())
	if err != nil {
		t.Fatal(err)
	}

	t.Log("URL:", dest)

	resp, err := http.Get(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		t.Fail()
	}
}

func TestWayback(t *testing.T) {
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

	cmd := exec.Command(binPath, "--headless", "--disable-gpu", "--no-sandbox", "--remote-debugging-port=9222", "--remote-debugging-address=0.0.0.0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start Chromium headless failed: %v", err)
	}
	go func() {
		// nolint:errcheck
		cmd.Wait()
	}()

	// Waiting for browser startup
	time.Sleep(3 * time.Second)
	defer func() {
		if err := cmd.Process.Kill(); err != nil {
			t.Errorf("Failed to kill process: %v", err)
		}
	}()

	input, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	arc := New().ByRemote("127.0.0.1:9222")
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
		t.Errorf("Unexpected wayback to telegra.ph by remote headless, got status code %d instead of 200", resp.StatusCode)
	}
}

func TestWaybackWithShots(t *testing.T) {
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
	if arc.Shot, err = screenshot.Screenshot(context.Background(), input, screenshot.Quality(100)); err != nil {
		t.Fatal(err)
	}
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
		t.Fail()
	}
	t.Log(paths)
}

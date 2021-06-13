// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package ph

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kallydev/telegraph-go"
	"github.com/oliamb/cutter"
	"github.com/wabarc/helper"
	"github.com/wabarc/imgbb"
	"github.com/wabarc/logger"
	"github.com/wabarc/screenshot"
)

type subject struct {
	title  []rune
	source string
}

type Archiver struct {
	Author string
	Shots  []screenshot.Screenshots

	client  *telegraph.Client
	subject subject

	browserRemoteAddr net.Addr
}

func init() {
	debug := os.Getenv("DEBUG")
	if debug == "true" || debug == "1" || debug == "on" {
		logger.EnableDebug()
	}
}

// New returns a Archiver struct.
func New() *Archiver {
	return &Archiver{}
}

// SetAuthor return an Archiver struct with Author
func (arc *Archiver) SetAuthor(author string) *Archiver {
	arc.Author = author
	return arc
}

// Shots return an Archiver struct with screenshot data
func (arc *Archiver) SetShots(s []screenshot.Screenshots) *Archiver {
	arc.Shots = s
	return arc
}

// Wayback is the handle of saving webpages to telegra.ph
func (arc *Archiver) Wayback(links []string) (map[string]string, error) {
	collect := make(map[string]string)
	var err error
	var matches []string
	for _, link := range links {
		if !helper.IsURL(link) {
			logger.Debug("[telegraph] " + link + " is invalid url.")
			continue
		}
		collect[link] = link
		matches = append(matches, link)
	}

	if len(collect) == 0 {
		logger.Debug("[telegraph] URL no found")
		return collect, fmt.Errorf("%s", "URL no found")
	}
	client, err := arc.newClient()
	if err != nil {
		logger.Debug("[telegraph] dial client failed: %v", err)
		return collect, err
	}
	arc.client = client

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if len(arc.Shots) == 0 {
		if arc.browserRemoteAddr != nil {
			addr := arc.browserRemoteAddr.(*net.TCPAddr)
			remote, er := screenshot.NewChromeRemoteScreenshoter(addr.String())
			if er != nil {
				logger.Debug("[telegraph] screenshot failed: %v", err)
				return collect, err
			}
			arc.Shots, err = remote.Screenshot(ctx, matches, screenshot.ScaleFactor(1))
		} else {
			arc.Shots, err = screenshot.Screenshot(ctx, matches, screenshot.Quality(100))
		}
		if err != nil {
			if err == context.DeadlineExceeded {
				logger.Debug("[telegraph] screenshot deadline: %v", err)
				return collect, err
			}
			logger.Debug("[telegraph] screenshot error: %v", err)
			return collect, err
		}
	}

	ch := make(chan string, len(collect))
	defer close(ch)

	for _, shot := range arc.Shots {
		if shot.URL == "" || shot.Image == nil {
			collect[shot.URL] = "Screenshots failed."
			logger.Debug("[telegraph] data empty")
			continue
		}
		name := helper.FileName(shot.URL, "image/png")
		file, err := ioutil.TempFile(os.TempDir(), "telegraph-*-"+name)
		if err != nil {
			logger.Debug("[telegraph] create temp dir failed: %v", err)
			continue
		}
		defer os.Remove(file.Name())

		if err := ioutil.WriteFile(file.Name(), shot.Image, 0o644); err != nil {
			logger.Debug("[telegraph] write image failed: %v", err)
			continue
		}

		if strings.TrimSpace(shot.Title) == "" {
			shot.Title = "Missing Title"
		}
		arc.subject = subject{title: []rune(shot.Title), source: shot.URL}
		go arc.post(file.Name(), ch)
		// Replace posted result in the map
		collect[shot.URL] = <-ch
	}

	return collect, nil
}

func (arc *Archiver) post(imgpath string, ch chan<- string) {
	if len(arc.subject.title) == 0 {
		ch <- "Title is required"
		return
	}
	if len(arc.subject.title) > 256 {
		arc.subject.title = arc.subject.title[:256]
	}

	// Telegraph image height limit upper 8976 px
	// crops, err := splitImage(imgpath, 8000)
	// if err != nil {
	// 	ch <- fmt.Sprintf("%v", err)
	// 	return
	// }

	// paths, err := arc.client.Upload(crops)
	// if err != nil {
	// 	ch <- fmt.Sprintf("%v", err)
	// 	return
	// }
	paths, er := upload(imgpath)
	if er != nil {
		ch <- fmt.Sprintf("%v", er)
		return
	}

	nodes := []telegraph.Node{}
	// nodes = append(nodes, "source: ")
	// nodes = append(nodes, telegraph.NodeElement{
	// 	Tag: "a",
	// 	Attrs: map[string]string{
	// 		"href":   arc.subject.source,
	// 		"target": "_blank",
	// 	},
	// 	Children: []telegraph.Node{arc.subject.source},
	// })
	for _, path := range paths {
		nodes = append(nodes, telegraph.NodeElement{
			Tag: "img",
			Attrs: map[string]string{
				"src": path,
				"alt": "Banner",
			},
		})
	}
	nodes = []telegraph.Node{
		telegraph.NodeElement{
			Tag:      "",
			Children: nodes,
		},
	}

	var pat bool
	var err error
	var page *telegraph.Page
	var title = string(arc.subject.title)
	if page, err = arc.client.CreatePage(title, nodes, nil); err != nil {
		// Create page with random path if title illegal previous
		if page, err = arc.client.CreatePage(helper.RandString(6, ""), nodes, nil); err != nil {
			logger.Error("[telegraph] create page failed: %v", err)
			ch <- "FAILED"
			return
		}
		pat = true
	}

	opts := &telegraph.EditPageOption{
		AuthorName:    "Source",
		AuthorURL:     arc.subject.source,
		ReturnContent: false,
	}
	if page, err = arc.client.EditPage(page.Path, title, nodes, opts); err != nil {
		logger.Error("[telegraph] edit page failed: %v", err)
		ch <- "FAILED"
		return
	}

	if pat {
		page.URL += "?title=" + url.PathEscape(title)
	}

	ch <- page.URL
}

func (arc *Archiver) newClient() (*telegraph.Client, error) {
	client, err := telegraph.NewClient("", nil)
	if err != nil {
		return nil, err
	}
	account, err := client.CreateAccount("telegraph-go", &telegraph.CreateAccountOption{
		AuthorName: "Anonymous",
		AuthorURL:  "https://example.org",
	})
	if err != nil {
		return nil, err
	}
	client.AccessToken = account.AccessToken

	return client, nil
}

func upload(filename string) (paths []string, err error) {
	url, err := imgbb.NewImgBB(nil, "").Upload(filename)
	if err != nil {
		return paths, err
	}

	return []string{url}, nil
}

func splitImage(name string, height int) (paths []string, err error) {
	rd, err := os.Open(name)
	if err != nil {
		return paths, err
	}
	defer rd.Close()

	dim, _, err := image.DecodeConfig(rd)
	if err != nil {
		return paths, err
	}

	if dim.Height <= height {
		return []string{name}, nil
	}

	img, err := readImage(name)
	if err != nil {
		return paths, err
	}

	round := float64(dim.Height) / float64(height)
	point := 0
	for round > 0 {
		simg, err := cutter.Crop(img, cutter.Config{
			Width:  dim.Width,
			Height: height,
			Anchor: image.Point{0, point},
		})
		if err != nil {
			logger.Debug("[telegraph] crop image failed: %v", err)
			return paths, err
		}

		if dim.Height-point < height {
			point += dim.Height - point
		} else {
			point += height
		}

		file, err := ioutil.TempFile(os.TempDir(), "telegraph-*.png")
		if err != nil {
			logger.Debug("[telegraph] create tmp dir failed: %v", err)
			continue
		}
		if err := writeImage(simg, file.Name()); err != nil {
			logger.Debug("[telegraph] write image failed: %v", err)
			continue
		}
		paths = append(paths, file.Name())
		round--
	}

	return paths, nil
}

func readImage(name string) (image.Image, error) {
	rd, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer rd.Close()

	img, _, err := image.Decode(rd)
	if err != nil {
		return nil, err
	}

	return img, nil
}

func writeImage(img image.Image, name string) error {
	fd, err := os.Create(name)
	if err != nil {
		return err
	}
	defer fd.Close()

	return png.Encode(fd, img)
}

// ByRemote returns Archiver with headless browser remote address.
func (arc *Archiver) ByRemote(addr string) *Archiver {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		logger.Debug("[telegraph] try to connect headless browser failed: %v", err)
	}
	if conn != nil {
		conn.Close()
		arc.browserRemoteAddr = conn.RemoteAddr()
		logger.Debug("[telegraph] connected: %v", conn.RemoteAddr().String())
	} else {
		logger.Debug("[telegraph] connect failed")
	}

	return arc
}

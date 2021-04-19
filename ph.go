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
	title  string
	source string
}

type Archiver struct {
	Author string

	client  *telegraph.Client
	subject subject
}

func init() {
	if os.Getenv("DEBUG") != "" {
		logger.EnableDebug()
	}
}

// Wayback is the handle of saving webpages to telegra.ph
func (arc *Archiver) Wayback(links []string) (map[string]string, error) {
	collect := make(map[string]string)
	var matches []string
	for _, link := range links {
		if !helper.IsURL(link) {
			logger.Debug(link + " is invalid url.")
			continue
		}
		collect[link] = link
		matches = append(matches, link)
	}

	if len(collect) == 0 {
		logger.Debug("URL no found")
		return collect, fmt.Errorf("%s", "URL no found")
	}
	client, err := arc.newClient()
	if err != nil {
		logger.Debug("%v", err)
		return collect, err
	}
	arc.client = client

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	shots, err := screenshot.Screenshot(ctx, matches, screenshot.Quality(100))
	if err != nil {
		if err == context.DeadlineExceeded {
			logger.Debug("%v", err)
			return collect, err
		}
		logger.Debug("%v", err)
		return collect, err
	}

	ch := make(chan string, len(collect))
	defer close(ch)

	for _, shot := range shots {
		if shot.URL == "" || shot.Data == nil {
			collect[shot.URL] = "Screenshots failed."
			logger.Debug("Data empty")
			continue
		}
		name := helper.FileName(shot.URL, "image/png")
		file, err := ioutil.TempFile(os.TempDir(), "telegraph-*-"+name)
		if err != nil {
			logger.Debug("%v", err)
			continue
		}
		defer os.Remove(file.Name())

		if err := ioutil.WriteFile(file.Name(), shot.Data, 0o644); err != nil {
			logger.Debug("%v", err)
			continue
		}

		if strings.TrimSpace(shot.Title) == "" {
			shot.Title = "Missing Title"
		}
		arc.subject = subject{title: shot.Title, source: shot.URL}
		go arc.post(file.Name(), ch)
		// Replace posted result in the map
		collect[shot.URL] = <-ch
	}

	return collect, nil
}

func (arc *Archiver) post(imgpath string, ch chan<- string) {
	if arc.subject.title == "" {
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
	paths, err := upload(imgpath)
	if err != nil {
		ch <- fmt.Sprintf("%v", err)
		return
	}

	nodes := []telegraph.Node{}
	nodes = append(nodes, "source: ")
	nodes = append(nodes, telegraph.NodeElement{
		Tag: "a",
		Attrs: map[string]string{
			"href":   arc.subject.source,
			"target": "_blank",
		},
		Children: []telegraph.Node{arc.subject.source},
	})
	for _, path := range paths {
		nodes = append(nodes, telegraph.NodeElement{
			Tag: "img",
			Attrs: map[string]string{
				"src": path,
				"alt": "Banner",
			},
		})
	}

	page, err := arc.client.CreatePage(arc.subject.title, []telegraph.Node{
		telegraph.NodeElement{
			Tag:      "",
			Children: nodes,
		},
	}, &telegraph.CreatePageOption{
		ReturnContent: false,
	})
	if err != nil {
		ch <- fmt.Sprintf("%v", err)
		return
	}

	ch <- page.URL
}

func (arc *Archiver) newClient() (*telegraph.Client, error) {
	client, err := telegraph.NewClient("", nil)
	if err != nil {
		return nil, err
	}
	// TODO: random name
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
			logger.Debug("%v", err)
			return paths, err
		}

		if dim.Height-point < height {
			point += dim.Height - point
		} else {
			point += height
		}

		file, err := ioutil.TempFile(os.TempDir(), "telegraph-*.png")
		if err != nil {
			logger.Debug("%v", err)
			continue
		}
		if err := writeImage(simg, file.Name()); err != nil {
			logger.Debug("%v", err)
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

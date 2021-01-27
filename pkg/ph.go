// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package ph

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/kallydev/telegraph-go"
	"github.com/wabarc/helper"
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

// Wayback is the handle of saving webpages to telegra.ph
func (arc *Archiver) Wayback(links []string) (map[string]string, error) {
	collect := make(map[string]string)
	var matches []string
	for _, link := range links {
		if !helper.IsURL(link) {
			log.Println(link + " is invalid url.")
			continue
		}
		collect[link] = link
		matches = append(matches, link)
	}

	if len(collect) == 0 {
		log.Println("URL no found")
		return collect, fmt.Errorf("%s", "URL no found")
	}
	client, err := arc.newClient()
	if err != nil {
		log.Println(err)
		return collect, err
	}
	arc.client = client

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	shots, err := screenshot.Screenshot(ctx, matches)
	if err != nil {
		if err == context.DeadlineExceeded {
			log.Println(err)
			return collect, err
		}
		log.Println(err)
		return collect, err
	}

	ch := make(chan string, len(collect))
	defer close(ch)

	for _, shot := range shots {
		if shot.URL == "" || shot.Data == nil {
			log.Println("Data empty")
			continue
		}
		name := helper.FileName(shot.URL, "image/png")
		file, err := ioutil.TempFile(os.TempDir(), "telegraph-"+name)
		if err != nil {
			log.Println(err)
			continue
		}
		defer os.Remove(file.Name())

		if err := ioutil.WriteFile(file.Name(), shot.Data, 0o644); err != nil {
			log.Println(err)
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

	paths, err := arc.client.Upload([]string{imgpath})
	if err != nil {
		ch <- fmt.Sprintf("%v", err)
		return
	}
	page, err := arc.client.CreatePage(arc.subject.title, []telegraph.Node{
		telegraph.NodeElement{
			Tag: "",
			Children: []telegraph.Node{
				"source: ",
				telegraph.NodeElement{
					Tag: "a",
					Attrs: map[string]string{
						"href":   arc.subject.source,
						"target": "_blank",
					},
					Children: []telegraph.Node{arc.subject.source},
				},
				telegraph.NodeElement{
					Tag: "img",
					Attrs: map[string]string{
						"src": paths[0],
						"alt": "Banner",
					},
				},
			},
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

// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package ph

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gabriel-vasile/mimetype"
	"github.com/go-shiori/go-readability"
	"github.com/kallydev/telegraph-go"
	"github.com/oliamb/cutter"
	"github.com/pkg/errors"
	"github.com/wabarc/helper"
	"github.com/wabarc/imgbb"
	"github.com/wabarc/logger"
	"github.com/wabarc/screenshot"
	"golang.org/x/net/html"
)

type subject struct {
	title  []rune
	source string
}

type Archiver struct {
	sync.RWMutex

	Author   string
	Shot     screenshot.Screenshots
	Articles map[string]readability.Article

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

// Shot return an Archiver struct with screenshot data
func (arc *Archiver) SetShot(s screenshot.Screenshots) *Archiver {
	arc.Shot = s
	return arc
}

// Wayback is the handle of saving webpages to telegra.ph
func (arc *Archiver) Wayback(ctx context.Context, input *url.URL) (dst string, err error) {
	client, err := arc.newClient()
	if err != nil {
		return "", errors.Wrap(err, `dial client failed`)
	}
	arc.client = client

	if arc.Shot.HTML == nil {
		opts := []screenshot.ScreenshotOption{
			screenshot.ScaleFactor(1),
			screenshot.RawHTML(true),
			screenshot.Quality(100),
		}
		if arc.browserRemoteAddr != nil {
			addr := arc.browserRemoteAddr.(*net.TCPAddr)
			remote, er := screenshot.NewChromeRemoteScreenshoter(addr.String())
			if er != nil {
				return dst, errors.Wrap(err, `screenshot failed`)
			}
			arc.Shot, err = remote.Screenshot(ctx, input, opts...)
		} else {
			arc.Shot, err = screenshot.Screenshot(ctx, input, opts...)
		}
		if err != nil {
			if err == context.DeadlineExceeded {
				return dst, errors.Wrap(err, `screenshot deadline`)
			}
			return dst, errors.Wrap(err, `screenshot error`)
		}
	}

	shot := arc.Shot
	if shot.HTML == nil {
		return "", errors.New("missing raw html")
	}

	if shot.URL == "" || shot.Image == nil {
		return "", errors.New("data empty")
	}

	name := helper.FileName(shot.URL, "image/png")
	file, err := ioutil.TempFile(os.TempDir(), "telegraph-*-"+name)
	if err != nil {
		return "", errors.Wrap(err, `create temp dir failed`)
	}
	defer os.Remove(file.Name())

	if err := ioutil.WriteFile(file.Name(), shot.Image, 0o644); err != nil {
		return "", errors.Wrap(err, `write image failed`)
	}

	arc.RLock()
	article := arc.Articles[shot.URL]
	arc.RUnlock()
	if article.Content != "" {
		goto post
	}

	article, err = readability.FromReader(bytes.NewReader(shot.HTML), input)
	if err != nil {
		goto post
	}
	if strings.TrimSpace(shot.Title) == "" {
		shot.Title = "Missing Title"
	}

post:
	arc.subject = subject{title: []rune(shot.Title), source: shot.URL}
	dst, err = arc.post(article.Content, file.Name())
	if err != nil {
		return "", err
	}

	return dst, nil
}

func (arc *Archiver) post(content, imgpath string) (dst string, err error) {
	if len(arc.subject.title) == 0 {
		return "", fmt.Errorf("Title is required")
	}
	if len(arc.subject.title) > 256 {
		arc.subject.title = arc.subject.title[:256]
	}

	// Telegraph image height limit upper 8976 px
	// crops, err := splitImage(imgpath, 8000)
	// if err != nil {
	// 	return "", err
	// }

	// paths, err := arc.client.Upload(crops)
	// if err != nil {
	// 	return "", err
	// }
	paths, er := upload(imgpath)
	if er != nil {
		return "", er
	}

	nodes := []telegraph.Node{}
	if content == "" {
		for _, path := range paths {
			nodes = append(nodes, telegraph.NodeElement{
				Tag: "img",
				Attrs: map[string]string{
					"src": path,
					"alt": "",
				},
			})
		}
		nodes = []telegraph.Node{
			telegraph.NodeElement{
				Tag:      "p",
				Children: nodes,
			},
		}
	} else {
		nodes = append(nodes, "screenshots: ")
		for i, path := range paths {
			nodes = append(nodes, telegraph.NodeElement{
				Tag: "a",
				Attrs: map[string]string{
					"href":   path,
					"target": "_blank",
				},
				Children: []telegraph.Node{strconv.Itoa(i + 1)},
			})
		}
		nodes = []telegraph.Node{
			telegraph.NodeElement{
				Tag:      "em",
				Children: nodes,
			},
			telegraph.NodeElement{
				Tag: "br",
			},
		}
	}

	// TODO: improvement for node large than 64 KB
	logger.Debug("[telegraph] content: %#v", content)
	if doc, err := goquery.NewDocumentFromReader(strings.NewReader(content)); err == nil {
		nodes = append(nodes, telegraph.NodeElement{
			Tag:      "p",
			Children: castNodes(traverseNodes(doc.Contents(), arc.client)),
		})
	}

	var pat bool
	var page *telegraph.Page
	var title = string(arc.subject.title)
	if page, err = arc.client.CreatePage(title, nodes, nil); err != nil {
		// Create page with random path if title illegal previous
		if page, err = arc.client.CreatePage(helper.RandString(6, ""), nodes, nil); err != nil {
			return "", errors.Wrap(err, `create page failed`)
		}
		pat = true
	}

	opts := &telegraph.EditPageOption{
		AuthorName:    "Source",
		AuthorURL:     arc.subject.source,
		ReturnContent: false,
	}
	if page, err = arc.client.EditPage(page.Path, title, nodes, opts); err != nil {
		return "", errors.Wrap(err, `edit page failed`)
	}

	if pat {
		page.URL += "?title=" + url.PathEscape(title)
	}

	return page.URL, nil
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
		return paths, errors.Wrap(err, `upload image to imgbb failed`)
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
			return paths, errors.Wrap(err, `crop image failed`)
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

// copied from: https://github.com/meinside/telegraph-go/blob/8b212a807f0302374ab467d61011e9aa5d26fbd1/methods.go#L402
func traverseNodes(selections *goquery.Selection, client *telegraph.Client) (nodes []telegraph.Node) {
	var tag string
	var attrs map[string]string
	var element telegraph.NodeElement

	selections.Each(func(_ int, child *goquery.Selection) {
		for _, node := range child.Nodes {
			switch node.Type {
			case html.TextNode:
				if len(strings.TrimSpace(node.Data)) > 0 {
					nodes = append(nodes, html.EscapeString(node.Data))
				}
			case html.ElementNode:
				attrs = map[string]string{}
				for _, attr := range node.Attr {
					// Upload image to telegra.ph
					if attr.Key == "src" || attr.Key == "data-src" {
						if newurl := uploadImage(client, attr.Val); newurl != "" {
							attr.Val = newurl
						}
					}
					attrs[attr.Key] = attr.Val
				}
				if len(node.Namespace) > 0 {
					tag = fmt.Sprintf("%s.%s", node.Namespace, node.Data)
				} else {
					tag = node.Data
				}
				element = telegraph.NodeElement{
					Tag:      tag,
					Attrs:    attrs,
					Children: traverseNodes(child.Contents(), client),
				}
				nodes = append(nodes, element)
			}
		}
	})

	return
}

func castNodes(nodes []telegraph.Node) (castNodes []telegraph.Node) {
	for _, node := range nodes {
		switch node.(type) {
		case telegraph.NodeElement:
			castNodes = append(castNodes, node)
		default:
			if cast, ok := node.(string); ok {
				castNodes = append(castNodes, cast)
			} else {
				logger.Error("param casting error: %#v", node)
			}
		}
	}

	return castNodes
}

func download(u *url.URL) (path string, err error) {
	path = filepath.Join(os.TempDir(), helper.RandString(21, "lower"))
	fd, err := os.Create(path)
	if err != nil {
		return path, err
	}
	defer fd.Close()

	resp, err := http.Get(u.String())
	if err != nil {
		return path, err
	}
	defer resp.Body.Close()

	if _, err = io.Copy(fd, resp.Body); err != nil {
		return path, err
	}

	return path, nil
}

func uploadImage(client *telegraph.Client, s string) (newurl string) {
	logger.Debug("[telegraph] uri: %s", s)
	u, err := url.Parse(s)
	if err != nil {
		logger.Error("[telegraph] parse url failed: %v", err)
		return newurl
	}

	path, err := download(u)
	if err != nil {
		logger.Error("[telegraph] download image failed: %v", err)
		return newurl
	}
	defer os.Remove(path)
	logger.Debug("[telegraph] downloaded image path: %s", path)

	mtype, err := mimetype.DetectFile(path)
	if os.IsNotExist(err) {
		logger.Warn("[telegraph] file %s not exist", path)
		return newurl
	}

	logger.Debug("[telegraph] content type: %s", mtype.String())
	if mtype.Is("image/webp") {
		dst := path + ".png"
		if err := helper.WebPToPNG(path, dst); err != nil {
			logger.Error("[telegraph] convert webp failed: %v", err)
		} else {
			defer os.Remove(dst)
			logger.Debug("[telegraph] converted image path: %s", dst)
			path = dst
		}
	}

	paths, err := client.Upload([]string{path})
	if err != nil || len(paths) == 0 {
		logger.Error("[telegraph] upload image %s content-type %s failed: %v", path, mtype, err)
		return newurl
	}
	newurl = paths[0] + "?orig=" + s
	logger.Debug("[telegraph] new uri: %s", newurl)

	return newurl
}

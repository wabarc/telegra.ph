// Copyright 2021 Wayback Archiver. All rights reserved.
// Use of this source code is governed by the GNU GPL v3
// license that can be found in the LICENSE file.

package ph

import (
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
	"github.com/cenkalti/backoff/v4"
	"github.com/gabriel-vasile/mimetype"
	"github.com/go-shiori/go-readability"
	"github.com/go-shiori/obelisk"
	"github.com/kallydev/telegraph-go"
	"github.com/oliamb/cutter"
	"github.com/pkg/errors"
	"github.com/wabarc/helper"
	"github.com/wabarc/imgbb"
	"github.com/wabarc/logger"
	"github.com/wabarc/screenshot"
	"golang.org/x/net/html"
)

const (
	maxElapsedTime = 5 * time.Minute
	maxRetries     = 10
	perm           = 0644
	timeout        = 30 * time.Second
)

type subject struct {
	title  []rune
	source string
}

type Archiver struct {
	sync.RWMutex

	Author string

	Client *http.Client

	// TODO: add http.Client to upstream
	client *telegraph.Client

	browserRemoteAddr net.Addr
}

func init() {
	debug := os.Getenv("DEBUG")
	if debug == "true" || debug == "1" || debug == "on" {
		logger.EnableDebug()
	}
}

// New returns a Archiver struct.
func New(client *http.Client) *Archiver {
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &Archiver{Client: client}
}

// SetAuthor return an Archiver struct with Author
func (arc *Archiver) SetAuthor(author string) *Archiver {
	arc.Author = author
	return arc
}

type ctxKeyShot struct{}

// WithShot puts a screenshot.Screenshots into context.
func (arc *Archiver) WithShot(ctx context.Context, shot *screenshot.Screenshots[screenshot.Path]) context.Context {
	return context.WithValue(ctx, ctxKeyShot{}, shot)
}

func shotFromContext(ctx context.Context) *screenshot.Screenshots[screenshot.Path] {
	if shot, ok := ctx.Value(ctxKeyShot{}).(*screenshot.Screenshots[screenshot.Path]); ok {
		return shot
	}
	return &screenshot.Screenshots[screenshot.Path]{}
}

type ctxKeyArticle struct{}

// WithArticle puts a readability.Article into context.
func (arc *Archiver) WithArticle(ctx context.Context, art readability.Article) context.Context {
	return context.WithValue(ctx, ctxKeyArticle{}, art)
}

func articleFromContext(ctx context.Context) readability.Article {
	if art, ok := ctx.Value(ctxKeyArticle{}).(readability.Article); ok {
		return art
	}
	return readability.Article{}
}

// Wayback is the handle of saving webpages to telegra.ph
func (arc *Archiver) Wayback(ctx context.Context, input *url.URL) (dst string, err error) {
	client, err := arc.newClient()
	if err != nil {
		return "", errors.Wrap(err, `dial client failed`)
	}
	arc.client = client

	dirname, err := os.MkdirTemp(os.TempDir(), "telegraph")
	if err != nil {
		return dst, err
	}
	defer os.RemoveAll(dirname)

	shot := shotFromContext(ctx)
	if shot.HTML == "" {
		file := screenshot.Files{
			HTML:  filepath.Join(dirname, "telegraph.html"),
			Image: filepath.Join(dirname, "telegraph.png"),
		}
		opts := []screenshot.ScreenshotOption{
			screenshot.AppendToFile(file),
			screenshot.ScaleFactor(1),
			screenshot.RawHTML(true),
			screenshot.Quality(100),
		}
		if arc.browserRemoteAddr != nil {
			addr := arc.browserRemoteAddr.(*net.TCPAddr)
			remote, er := screenshot.NewChromeRemoteScreenshoter[screenshot.Path](addr.String())
			if er != nil {
				return dst, errors.Wrap(err, `screenshot failed`)
			}
			shot, err = remote.Screenshot(ctx, input, opts...)
		} else {
			shot, err = screenshot.Screenshot[screenshot.Path](ctx, input, opts...)
		}
		if err != nil {
			if err == context.DeadlineExceeded {
				return dst, errors.Wrap(err, `screenshot deadline`)
			}
			return dst, errors.Wrap(err, `screenshot error`)
		}
	}

	if shot.HTML == "" {
		buf, err := arc.capture(ctx, input)
		if err != nil {
			return "", errors.Wrap(err, `capture webpage via obelisk failed`)
		}
		fp := filepath.Join(dirname, "telegraph.html")
		shot.HTML = screenshot.Path(fp)
		os.WriteFile(fp, buf, perm)
	}

	if shot.URL == "" || shot.Image == "" {
		return "", errors.New("data empty")
	}

	file, _ := os.Open(fmt.Sprint(shot.HTML))
	article := articleFromContext(ctx)
	if article.Content != "" {
		goto post
	}

	article, err = readability.FromReader(file, input)
	if err != nil {
		goto post
	}
	if strings.TrimSpace(shot.Title) == "" {
		shot.Title = "Missing Title"
	}

post:
	sub := subject{title: []rune(shot.Title), source: shot.URL}
	dst, err = arc.post(sub, article.Content, fmt.Sprint(shot.Image))
	if err != nil {
		return "", err
	}

	return dst, nil
}

func (arc *Archiver) capture(ctx context.Context, uri *url.URL) ([]byte, error) {
	req := obelisk.Request{URL: uri.String()}
	obe := &obelisk.Archiver{
		SkipResourceURLError: true,
	}
	obe.Validate()

	buf, _, err := obe.Archive(ctx, req)
	if err != nil {
		return nil, errors.Wrap(err, "archive failed")
	}
	return buf, nil
}

func (arc *Archiver) post(sub subject, content, imgpath string) (dst string, err error) {
	if len(sub.title) == 0 {
		return "", fmt.Errorf("Title is required")
	}
	if len(sub.title) > 256 {
		sub.title = sub.title[:255]
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
	paths, _ := arc.uploadImage(imgpath)

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
			Children: castNodes(arc.traverseNodes(doc.Contents())),
		})
	}

	var pat bool
	var page *telegraph.Page
	var title = string(sub.title)
	if page, err = arc.client.CreatePage(title, nodes, nil); err != nil {
		// Create page with random path if title illegal previous
		if page, err = arc.client.CreatePage(helper.RandString(6, ""), nodes, nil); err != nil {
			return "", errors.Wrap(err, `create page failed`)
		}
		pat = true
	}

	opts := &telegraph.EditPageOption{
		AuthorName:    "Source",
		AuthorURL:     sub.source,
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

func (arc *Archiver) uploadImage(fp string) ([]string, error) {
	paths, err := arc.client.Upload([]string{fp})
	if err != nil || len(paths) == 0 {
		paths, err = uploadToImgbb(fp)
		if err != nil || len(paths) == 0 {
			return paths, err
		}
	}
	return paths, err
}

func uploadToImgbb(filename string) (paths []string, err error) {
	url, err := imgbb.NewImgBB(nil, "").Upload(filename)
	if err != nil {
		return paths, errors.Wrap(err, fmt.Sprintf("upload image %s to ImgBB failed", filename))
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
func (arc *Archiver) traverseNodes(selections *goquery.Selection) (nodes []telegraph.Node) {
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
				mu := sync.Mutex{}
				ch := make(chan string)
				for _, attr := range node.Attr {
					// Upload image to telegra.ph or ImgBB
					if attr.Key == "src" || attr.Key == "data-src" {
						logger.Debug("transferring url: %s", attr.Val)
						go arc.transferImage(attr.Val, ch)
					}
				}
				// Assign transferred URI
				for _, attr := range node.Attr {
					if attr.Key == "src" || attr.Key == "data-src" {
						logger.Debug("waiting url: %s", attr.Val)
						newurl := <-ch
						if newurl != "" {
							logger.Debug("newn url: %s", newurl)
							attr.Val = newurl
						}
					}
					mu.Lock()
					attrs[attr.Key] = attr.Val
					mu.Unlock()
				}
				close(ch)

				if len(node.Namespace) > 0 {
					tag = fmt.Sprintf("%s.%s", node.Namespace, node.Data)
				} else {
					tag = node.Data
				}
				element = telegraph.NodeElement{
					Tag:      tag,
					Attrs:    attrs,
					Children: arc.traverseNodes(child.Contents()),
				}
				mu.Lock()
				nodes = append(nodes, element)
				mu.Unlock()
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

func (arc *Archiver) download(u *url.URL) (path string, err error) {
	if !helper.IsURL(u.String()) {
		return path, errors.Wrap(err, "invalid url")
	}

	path = filepath.Join(os.TempDir(), helper.RandString(21, "lower"))
	fd, err := os.Create(path)
	if err != nil {
		return path, err
	}
	defer fd.Close()

	resp, err := arc.Client.Get(u.String())
	if err != nil {
		return path, err
	}
	defer resp.Body.Close()

	if _, err = io.Copy(fd, resp.Body); err != nil {
		return path, err
	}

	return path, nil
}

// transferImage download image from original server and upload to Telegraph or ImgBB,
// it returns image path or full url.
func (arc *Archiver) transferImage(s string, c chan string) {
	logger.Debug("[telegraph] uri: %s", s)
	if strings.HasPrefix(s, "data:") {
		c <- ""
		return
	}

	u, err := url.Parse(s)
	if err != nil {
		logger.Error("parse uri failed: %v", err)
		c <- ""
		return
	}

	path, err := arc.download(u)
	if err != nil {
		logger.Error("download image failed: %v", err)
		c <- ""
		return
	}
	defer os.Remove(path)
	logger.Debug("[telegraph] downloaded image path: %s", path)

	mtype, err := mimetype.DetectFile(path)
	if os.IsNotExist(err) {
		logger.Error("file %s not exists", path)
		c <- ""
		return
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

	paths, err := arc.uploadImage(path)
	if err != nil || len(paths) == 0 {
		logger.Error("upload image failed: %v", err)
		c <- ""
		return
	}

	newurl := paths[0] + "?orig=" + s
	logger.Debug("[telegraph] new uri: %s", newurl)

	c <- newurl
}

func doRetry(op backoff.Operation) error {
	exp := backoff.NewExponentialBackOff()
	exp.MaxElapsedTime = maxElapsedTime
	bo := backoff.WithMaxRetries(exp, maxRetries)

	return backoff.Retry(op, bo)
}

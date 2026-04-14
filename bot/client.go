/*
Written by Efdal Sancak (aka z3ntl3)

github.com/z3ntl3

Disclaimer: Educational purposes only
License: GNU
*/
package bot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/corpix/uarand"
	"github.com/go-errors/errors"
	"github.com/z3ntl3/MolyRevProxy/globals"
	"github.com/z3ntl3/MolyRevProxy/models"
	"go.mongodb.org/mongo-driver/bson"
	"h12.io/socks"
)

var m3u8Pattern = regexp.MustCompile(`((https?:\\/\\/|https?://|\\/|/)[^"'\\s]+?\\.m3u8[^"'\\s]*)`)

func ObtainManifest(body []byte, pageURL string) (string, error) {
	candidate := m3u8Pattern.FindString(string(body))
	if candidate == "" {
		return "", errors.New("no manifest found")
	}

	candidate = strings.ReplaceAll(candidate, `\/`, `/`)
	candidate = strings.ReplaceAll(candidate, `\u002F`, `/`)

	if strings.HasPrefix(candidate, "//") {
		candidate = "https:" + candidate
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return "", err
	}

	manifestURL, err := url.Parse(candidate)
	if err != nil {
		return "", err
	}

	if !manifestURL.IsAbs() {
		manifestURL = base.ResolveReference(manifestURL)
	}

	if !strings.Contains(manifestURL.Path, ".m3u8") {
		return "", errors.New("no valid m3u8 manifest found")
	}

	return manifestURL.String(), nil
}

type Client struct {
	*http.Client
}

type ManifestCtx struct {
	Headers http.Header
	Raw     string
}

func NewClient(timeout time.Duration) *Client {
	return &Client{
		Client: &http.Client{
			Timeout: timeout,
			Jar:     http.DefaultClient.Jar,
		},
	}
}

/*
to unveil underlying m3u8 manifest
*/
func (c *Client) GetManifest(url string, init bool) (*ManifestCtx, error) {
	timeout := c.Client.Timeout
	if timeout <= 0 {
		timeout = time.Second * 5
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	workerPool := make(chan struct {
		Err error
		Ctx ManifestCtx
	}, 5)

	go func(pool chan struct {
		Err error
		Ctx ManifestCtx
	}, master bool,
	) {
		for i := 0; i < cap(pool); i++ {
			go func(ctx context.Context) {
				var err error
				var result ManifestCtx

				defer func(err_ *error, ctx_ *ManifestCtx) {
					pool <- struct {
						Err error
						Ctx ManifestCtx
					}{
						Err: *err_,
						Ctx: *ctx_,
					}
				}(&err, &result)

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				if err != nil {
					return
				}

				build_headers(req)
				res, err := c.Client.Do(req)
				if err != nil {
					return
				}
				defer res.Body.Close()

				body, err := io.ReadAll(res.Body)
				if err != nil {
					return
				}

				if res.StatusCode != http.StatusOK {
					err = errors.Errorf("status code '%d' with body %s", res.StatusCode, body)
					return
				}

				if !master {
					result.Headers = req.Header
					result.Raw = string(body)
					return
				}

				link, err := ObtainManifest(body, url)
				if err != nil {
					return
				}

				data, err := c.read_manifest(ctx, link)
				if err != nil {
					return
				}

				result.Headers = req.Header
				result.Raw = data
			}(ctx)
		}
	}(workerPool, init)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("manifest fetch canceled for %s: %w", url, ctx.Err())
		case task := <-workerPool:
			if task.Err != nil {
				continue
			}
			return &task.Ctx, nil
		}
	}
}

/*
To obtain data rapidly about the vidmoly stream
*/
func StreamCore(molyLink string) (*models.StreamData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	task := make(chan struct {
		Err error
		Res models.StreamData
	})

	go func(worker chan<- struct {
		Err error
		Res models.StreamData
	},
	) {
		var err error
		var res models.StreamData

		defer func(res_ *models.StreamData, err_ *error) {
			worker <- struct {
				Err error
				Res models.StreamData
			}{
				Err: *err_,
				Res: *res_,
			}
		}(&res, &err)

		err = globals.MongoClient.Collection(models.StreamCol).FindOne(ctx, bson.M{
			"$match": bson.M{
				"vidmoly_alias": molyLink,
			},
		}).Decode(&res)
	}(task)

	select {
	case v := <-task:
		return &v.Res, v.Err
	case <-ctx.Done():
		v := <-task
		return nil, v.Err
	}
}

func (c *Client) read_manifest(ctx context.Context, link string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", err
	}

	build_headers(req)
	res, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	if res.StatusCode != http.StatusOK {
		return "", errors.Errorf("status code '%d' with body %s", res.StatusCode, body)
	}

	return string(body), nil
}

func build_headers(req *http.Request) {
	req.Header.Add("User-Agent", uarand.GetRandom())
	req.Header.Add("Cache-Control", "no-store")
	req.Header.Add("Origin", "https://vidmoly.to")
	req.Header.Add("Referer", "https://vidmoly.to/")
}

func (c *Client) DelProxy() {
	c.Client.Transport = http.DefaultTransport
}

// socks 4/5 or http proxy
func (c *Client) SetProxy(proxyURI string) error {
	c.Client.Transport = &http.Transport{
		Dial: socks.Dial(proxyURI),
	}

	return nil
}

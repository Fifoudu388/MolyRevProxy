/*
Written by Efdal Sancak (aka z3ntl3)

github.com/z3ntl3

Disclaimer: Educational purposes only
License: GNU
*/
package routes

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-errors/errors"
	"github.com/grafov/m3u8"
	"github.com/spf13/viper"
	"github.com/z3ntl3/MolyRevProxy/bot"
)

func Manifest_Stream(ctx *gin.Context) {
	var err error
	var res []byte

	defer func(res_ *[]byte, err_ *error, ctx_ *gin.Context) {
		if *err_ != nil {
			fmt.Println(errors.New(err).ErrorStack())
			ctx.AbortWithStatusJSON(403, struct {
				Context string `json:"context"`
			}{
				Context: (*err_).Error(),
			})
			return
		}
		ctx_.Header("Content-Type", "application/vnd.apple.mpegurl")
		ctx_.Writer.Write(*res_)
	}(&res, &err, ctx)

	videoCtx := struct {
		URL string `validate:"required,vidmoly,max=300,min=5"`
	}{
		URL: ctx.Query("url"),
	}

	if err = binding.Validator.ValidateStruct(&videoCtx); err != nil {
		return
	}

	client := bot.NewClient(time.Second * 10)
	manifest, err := client.GetManifest(videoCtx.URL, true)
	if err != nil {
		return
	}

	master := m3u8.NewMasterPlaylist()
	if err = master.DecodeFrom(strings.NewReader(manifest.Raw), true); err == nil && len(master.Variants) > 0 {
		if err = manipulate(master); err != nil {
			return
		}
		res = master.Encode().Bytes()
		return
	}

	media, mediaErr := m3u8.NewMediaPlaylist(uint(20000), uint(20000))
	if mediaErr != nil {
		err = mediaErr
		return
	}

	err = media.DecodeFrom(strings.NewReader(manifest.Raw), true)
	if err != nil {
		return
	}

	if err = manipulate(media); err != nil {
		return
	}

	res = media.Encode().Bytes()
}

func manipulate(data interface{}) (err error) {
	master, isMaster := data.(*m3u8.MasterPlaylist)
	main, isMain := data.(*m3u8.MediaPlaylist)

	if !isMaster && !isMain {
		err = errors.New("either not master or main m3u8 manifest")
		fmt.Println(errors.New(err).ErrorStack())
		return
	}

	if isMain {
		for k, v := range main.Segments {
			if v == nil {
				continue
			}
			uri, err := url.Parse(v.URI)
			if err != nil {
				fmt.Println(errors.New(err).ErrorStack())
				return err
			}

			uri.Host = viper.GetStringMap("server")["domain"].(string)
			main.Segments[k].URI = uri.String()
		}
		return
	}

	for k, v := range master.Variants {
		if v == nil {
			continue
		}

		uri, err := url.Parse(v.URI)
		if err != nil {
			fmt.Println(errors.New(err).ErrorStack())
			return err
		}

		uri.Host = viper.GetStringMap("server")["domain"].(string)
		master.Variants[k].URI = uri.String()
	}
	return
}

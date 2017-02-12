package main

import (
	"bytes"
	"errors"
	"github.com/golang/groupcache"
	"github.com/nfnt/resize"
	_ "golang.org/x/image/webp"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
)

type Renderer struct {
	Loader SourceLoader

	MaxWidth  int
	MaxHeight int
	MaxInDim  int

	JpegQuality int
}

func (r *Renderer) Get(ctx groupcache.Context, key string, dest groupcache.Sink) error {
	data, err := r.Render(key)
	if err == nil {
		atomic.AddUint64(&status.ThumbsGenerated, 1)
		dest.SetBytes(data)
	} else {
		atomic.AddUint64(&status.ThumbsFailed, 1)
	}
	return err
}

var RenderWrongUrl = errors.New("Wrong url format.")
var RenderWrongPrefix = errors.New("Wrong action. Only 'fit-in' is supported")
var RenderWrongSize = errors.New("Wrong size.")
var RenderTooLarge = errors.New("One of image dimensions is too large.")

func (r *Renderer) Render(url string) ([]byte, error) {
	splitUrl := strings.SplitN(url, "/", 3)
	if len(splitUrl) != 3 {
		return nil, RenderWrongUrl
	}
	if splitUrl[0] != "fit-in" {
		return nil, RenderWrongPrefix
	}
	splitSize := strings.SplitN(splitUrl[1], "x", 2)
	if len(splitSize) != 2 {
		return nil, RenderWrongSize
	}

	width := parseOrDefault(splitSize[0], 1, r.MaxWidth, r.MaxWidth)
	height := parseOrDefault(splitSize[1], 1, r.MaxHeight, r.MaxHeight)

	data, err := r.Loader.Load(splitUrl[2])
	if err != nil {
		return nil, err
	}

	bufreader := bytes.NewReader(data)
	config, format, err := image.DecodeConfig(bufreader)
	if err != nil {
		return nil, err
	}
	if config.Width > r.MaxInDim || config.Height > r.MaxInDim {
		return nil, RenderTooLarge
	}

	bufreader.Seek(0, 0)
	img, _, err := image.Decode(bufreader)
	if err != nil {
		return nil, err
	}

	thumb := resize.Thumbnail(uint(width), uint(height), img, resize.NearestNeighbor)
	var outbuf bytes.Buffer
	encodeImage(&outbuf, thumb, format, r.JpegQuality)
	log.Printf("Rendered (%dx%d %s) -> (%dx%d): %s", config.Width, config.Height, format, width, height, splitUrl[2])
	return outbuf.Bytes(), nil
}

func parseOrDefault(s string, min int, max int, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	switch {
	case err != nil:
		return def
	case int(n) < min:
		return min
	case int(n) > max:
		return max
	}
	return int(n)
}

func encodeImage(w io.Writer, img image.Image, fmt string, jq int) error {
	switch fmt {
	case "png":
		return png.Encode(w, img)
	case "gif":
		return gif.Encode(w, img, &gif.Options{NumColors: 256})
	}
	return jpeg.Encode(w, img, &jpeg.Options{Quality: jq})
}

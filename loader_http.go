package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

type HTTPLoader struct {
	hc      http.Client
	maxsize int64
}

type HTTPLoaderError struct {
	Where string
	What  error
	URL   string
}

func (le *HTTPLoaderError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", le.Where, le.What.Error(), le.URL)
}

func NewHTTPLoader(args map[string]string) SourceLoader {
	itimeout, err := strconv.ParseInt(args["timeout"], 10, 64)
	if err != nil {
		log.Printf("HttpLoader: Unable to parse timeout value '%s', using default of 10 seconds.", args["timeout"])
		itimeout = 10
	}
	maxSize, err := strconv.ParseInt(args["max_size"], 10, 64)
	if err != nil {
		log.Printf("HttpLoader: Unable to parse max size value '%s', using 20 Mb.", args["max_size"])
		maxSize = 20 * 1024 * 1024
	}
	timeout := time.Duration(itimeout) * time.Second

	l := &HTTPLoader{}
	l.maxsize = maxSize
	l.hc = http.Client{
		Timeout: timeout,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
			ResponseHeaderTimeout: timeout,
			TLSHandshakeTimeout:   timeout,
			Dial: func(network, address string) (net.Conn, error) {
				return net.DialTimeout(network, address, timeout)
			},
		},
	}
	return l
}

func (l *HTTPLoader) Load(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, &HTTPLoaderError{"NewRequest", err, url}
	}

	req.Header.Set("User-Agent", "bnw-thumb/1.1 (http://github.com/stiletto/bnw-thumb)")
	resp, err := l.hc.Do(req)
	if err != nil {
		return nil, &HTTPLoaderError{"Do", err, url}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, &HTTPLoaderError{"NotOk", fmt.Errorf("%d", resp.StatusCode), url}
	}
	if resp.ContentLength > l.maxsize {
		return nil, &HTTPLoaderError{"TooBig", fmt.Errorf("%d", resp.ContentLength), url}
	}

	limiter := &io.LimitedReader{R: resp.Body, N: l.maxsize + 1}
	bytebuf, err := ioutil.ReadAll(limiter)
	if err != nil {
		return nil, &HTTPLoaderError{"ReadAll", err, url}
	}
	if limiter.N == 0 {
		return nil, &HTTPLoaderError{"TooBig", errors.New(strconv.Itoa(int(l.maxsize + 1))), url}
	}

	return bytebuf, nil
}

func init() {
	RegisterLoader("http", NewHTTPLoader)
}
